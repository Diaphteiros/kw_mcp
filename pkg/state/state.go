package state

import (
	"fmt"

	libcontext "github.com/Diaphteiros/kw/pluginlib/pkg/context"
	"github.com/Diaphteiros/kw/pluginlib/pkg/debug"
	liberrors "github.com/Diaphteiros/kw/pluginlib/pkg/errors"
	libstate "github.com/Diaphteiros/kw/pluginlib/pkg/state"
	"sigs.k8s.io/yaml"
)

type MCPState struct {
	Focus Focus `json:"focus"`
}

// String returns a YAML representation of the state.
func (s *MCPState) YAML() ([]byte, error) {
	data, err := yaml.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("error marshaling MCP state to yaml: %w", err)
	}
	return data, nil
}

func (s *MCPState) Id(pluginName string) string {
	return s.Focus.ID(pluginName)
}

func (s *MCPState) Notification() string {
	return s.Focus.Notification()
}

// Load fills the receiver state object with the data from the kubeswitcher state.
// The first return value is true if any state was actually loaded, false otherwise.
func (s *MCPState) Load(con *libcontext.Context) (bool, error) {
	debug.Debug("Loading MCP state")
	ts, err := libstate.LoadTypedState[*MCPState](con.GenericStatePath, con.PluginStatePath, con.CurrentPluginName)
	if err != nil {
		return false, liberrors.IgnoreStateFromAnotherPluginError(fmt.Errorf("error loading kubeswitcher state: %w", err))
	}
	if ts == nil || ts.PluginState == nil {
		return false, nil
	}
	s.Focus = ts.PluginState.Focus
	return true, nil
}
