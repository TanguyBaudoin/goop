package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// State is what bootstrap remembers between runs, so that running it
// twice is not the same as running it once with different intent.
//
// Without it there is no way to tell "this profile member was never
// installed" from "it was installed and then deliberately removed" --
// and bootstrap would helpfully reinstall, every time, the thing someone
// took off their machine on purpose.
type State struct {
	// Applied records, per profile, the packages bootstrap has installed.
	Applied map[string][]string `json:"applied,omitempty"`

	// IDE is the chosen editor. IDEAsked marks that the question has been
	// put at all, so declining is remembered rather than re-asked on
	// every run.
	IDE      string `json:"ide,omitempty"`
	IDEAsked bool   `json:"ide_asked,omitempty"`
}

func statePath() string { return filepath.Join(profilesDir(), "state.json") }

// LoadState reads bootstrap state. A machine that has never run it has
// empty state, not an error.
func LoadState() State {
	data, err := os.ReadFile(statePath())
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	return s
}

// SaveState persists bootstrap state.
func SaveState(s State) error {
	if err := os.MkdirAll(profilesDir(), 0o755); err != nil {
		return err
	}
	for name, apps := range s.Applied {
		sort.Strings(apps)
		s.Applied[name] = apps
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), append(data, '\n'), 0o644)
}

// WasApplied reports whether bootstrap has previously installed app as
// part of profileName. An app that was applied and is now gone was
// removed on purpose, and must not come back.
func (s State) WasApplied(profileName, app string) bool {
	for _, a := range s.Applied[profileName] {
		if a == app {
			return true
		}
	}
	return false
}

// MarkApplied records that app was installed for profileName.
func (s *State) MarkApplied(profileName, app string) {
	if s.Applied == nil {
		s.Applied = map[string][]string{}
	}
	if s.WasApplied(profileName, app) {
		return
	}
	s.Applied[profileName] = append(s.Applied[profileName], app)
}
