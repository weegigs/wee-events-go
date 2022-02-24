# Conventions

This document covers a number of the conventions (the ones I consciously follow, there are probably more that I'm unconscious of).

## Logging

The logging conventions in use are inspired by Dave Cheney's [Let’s talk about logging][].
I encourage you to read it, it'll only take a few minutes and it's worthwhile.

Basically it boils down to this. There are two levels of log entry, `info` and `debug`
That's it. No more no less.

### `info`

Things that users care about when using your software.

> Info should simply write that line to the log output. There should not be an option to
> turn it off as the user should only be told things which are useful for them.

The messages you write in `info` logging are a function output. Let me say that again,
`info` logging is a function output.

The log output is an implicit contract with the user of your application and needs to
be recognized as such. There will be systems downstream parsing your log output to extract details and create summaries.

If you change your log output you may invalidate your log consumers.

If you are emitting `info` level log messages then consider writing unit tests to ensure
their consistency and accuracy.

### `debug`

Things that developers care about when they are developing or debugging software.

> It is for the developer or support engineer to control. During development, debugging
> statements should be plentiful.

I would temper this with "can be plentiful".

Specifically:

- Don't use debug logging in place of a debugger.
- Don't use debug logging in place of a tracer.

Debug logging may be useful during development to quickly verify something, temporary and
to the point. If you're tempted to leave the debug in place consider adding the information
to the trace.

[let’s talk about logging]: https://dave.cheney.net/2015/11/05/lets-talk-about-logging