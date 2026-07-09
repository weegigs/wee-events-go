package werestate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

// TransportError is a boundary-lane failure: the call never produced a
// declared service outcome. Network loss, an unreachable ingress, a non-frame
// failure body, or an undecodable response all land here. It is deliberately a
// distinct type from any declared error so callers branch with errors.As and
// transport concerns are never folded into a service's error contract (the
// Declared-vs-Transport separation from the Rust execution-model addendum,
// rendered as plain Go error types).
type TransportError struct {
	// Status is the ingress HTTP status; 0 when the request never completed.
	Status int
	// Message is the transport-level failure detail.
	Message string
	cause   error
}

func (e *TransportError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("werestate: transport failure (status %d): %s", e.Status, e.Message)
	}
	return "werestate: transport failure: " + e.Message
}

func (e *TransportError) Unwrap() error { return e.cause }

// FrameDecoder maps a decoded error frame to a service-specific declared
// error. It returns ok=false to pass the frame to the next decoder; a frame no
// decoder claims falls back to the generic we.Rejection carrying the frame's
// code, message, and fields.
type FrameDecoder func(we.ErrorFrame) (error, bool)

// Client is the typed boundary handle for a werestate service reached through
// Restate ingress. It speaks the ingress HTTP API directly because the SDK's
// ingress client flattens terminal failures into opaque strings; owning the
// HTTP exchange is what lets declared errors and transport failures stay in
// separate lanes.
type Client struct {
	baseURL  string
	service  string
	http     *http.Client
	decoders []FrameDecoder
}

// ClientOption configures a Client before first use.
type ClientOption func(*Client)

// HTTPClient overrides the HTTP client used for ingress calls.
func HTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.http = client
	}
}

// Decoder appends a FrameDecoder consulted, in registration order, before the
// generic rejection fallback.
func Decoder(decoder FrameDecoder) ClientOption {
	return func(c *Client) {
		c.decoders = append(c.decoders, decoder)
	}
}

// NewClient builds a boundary client for one service registered with the
// Restate runtime at baseURL (the ingress address, e.g. "http://localhost:8080").
func NewClient(baseURL string, service string, options ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		service: service,
		http:    http.DefaultClient,
	}
	for _, option := range options {
		option(c)
	}
	if c.http == nil {
		c.http = http.DefaultClient
	}
	return c
}

// Load reads current entity state through the service's load handler.
func (c *Client) Load(ctx context.Context, id we.AggregateId) (EntityResponse, error) {
	return c.call(ctx, id, "load", nil)
}

// Execute dispatches a command through the service's execute handler.
func (c *Client) Execute(ctx context.Context, id we.AggregateId, command we.RemoteCommand) (EntityResponse, error) {
	body, err := json.Marshal(command)
	if err != nil {
		return EntityResponse{}, fmt.Errorf("werestate: encode remote command: %w", err)
	}
	return c.call(ctx, id, "execute", body)
}

// call performs one ingress exchange: POST {base}/{service}/{key}/{handler}.
func (c *Client) call(ctx context.Context, id we.AggregateId, handler string, body []byte) (EntityResponse, error) {
	key, err := EncodeKey(id)
	if err != nil {
		return EntityResponse{}, fmt.Errorf("werestate: invalid aggregate id: %w", err)
	}

	target := c.baseURL + "/" + url.PathEscape(c.service) + "/" + url.PathEscape(key) + "/" + url.PathEscape(handler)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, reader)
	if err != nil {
		return EntityResponse{}, fmt.Errorf("werestate: build ingress request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return EntityResponse{}, &TransportError{Message: err.Error(), cause: err}
	}
	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(response.Body)
	if err != nil {
		return EntityResponse{}, &TransportError{Status: response.StatusCode, Message: "unreadable response body: " + err.Error(), cause: err}
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return EntityResponse{}, c.classifyFailure(response.StatusCode, data)
	}

	var entity EntityResponse
	if err := json.Unmarshal(data, &entity); err != nil {
		return EntityResponse{}, &TransportError{Status: response.StatusCode, Message: "undecodable entity response: " + err.Error(), cause: err}
	}
	return entity, nil
}

// classifyFailure separates the two failure lanes: a failure body whose
// message carries an encoded error frame is a DECLARED service error and is
// decoded back into a branchable error value; anything else is a
// *TransportError. A frame always becomes a declared error — at minimum the
// generic we.Rejection — never a transport failure.
func (c *Client) classifyFailure(status int, body []byte) error {
	var failure struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	if err := json.Unmarshal(body, &failure); err != nil {
		return &TransportError{Status: status, Message: string(body)}
	}

	frame, ok := decodeErrorFrame(stripIngressDecoration(failure.Message))
	if !ok {
		return &TransportError{Status: status, Message: failure.Message}
	}

	for _, decode := range c.decoders {
		if declared, claimed := decode(frame); claimed {
			return declared
		}
	}
	return we.Rejection(frame)
}

// stripIngressDecoration removes the "[<code>] " prefix the Restate runtime
// prepends to a terminal error's message when rendering the ingress failure
// body. The decoration is a transport artifact of the ingress edge — it is
// not part of the error-frame contract, so it is stripped here rather than
// tolerated in the shared frame codec.
func stripIngressDecoration(message string) string {
	rest, ok := strings.CutPrefix(message, "[")
	if !ok {
		return message
	}
	digits, undecorated, found := strings.Cut(rest, "] ")
	if !found || digits == "" {
		return message
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return message
		}
	}
	return undecorated
}
