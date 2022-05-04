package we

import (
  "testing"

  "github.com/stretchr/testify/assert"
)

type TestCommand struct{}

type TestNamedCommand struct{}

func (TestNamedCommand) TypeName() string {
  return "test:named"
}

func resolvesExplicitName(t *testing.T) {
  assert.Equal(t, CommandName("test:named"), CommandNameOf(TestNamedCommand{}))
}

func resolvesImplicitName(t *testing.T) {
  assert.Equal(t, CommandName("we:test-command"), CommandNameOf(TestCommand{}))
}

func TestCommands(t *testing.T) {
  t.Run("resolves explicit name", resolvesExplicitName)
  t.Run("resolves implicit name", resolvesImplicitName)
}
