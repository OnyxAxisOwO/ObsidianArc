package settings

import (
	"fmt"
	"slices"
	"sort"
	"sync"
)

// What a plugin brings while the server runs — a package installed from the
// backoffice — as opposed to what a compiled-in one registers from init (see
// Define). The two differ in where they live: init runs once per process
// and writes package globals, which is right for something compiled in and
// wrong for something installed, because a process that builds several
// servers (the tests do) would show one server's install to the next, and
// because an install can be undone. So these belong to one Service.
type dynamicSet struct {
	mu    sync.RWMutex
	defs  map[string]Definition
	modes map[string]string
}

// AddPluginDefinitions makes a plugin's settings and sign-up challenge modes
// known to this service, replacing whatever the same plugin had added before
// — an update — in one step. Nothing changes unless everything can: a key or a
// mode that another owner already has refuses the whole call, which is the
// panic Define would have raised at init, arrived at as an error because
// there is somebody to tell.
func (s *Service) AddPluginDefinitions(plugin string, defs []Definition, modes []string) error {
	s.dyn.mu.Lock()
	defer s.dyn.mu.Unlock()
	if err := s.checkPluginDefinitions(plugin, defs, modes); err != nil {
		return err
	}
	if s.dyn.defs == nil {
		s.dyn.defs = map[string]Definition{}
		s.dyn.modes = map[string]string{}
	}
	for key, d := range s.dyn.defs {
		if d.Plugin == plugin {
			delete(s.dyn.defs, key)
		}
	}
	for mode, owner := range s.dyn.modes {
		if owner == plugin {
			delete(s.dyn.modes, mode)
		}
	}
	for _, d := range defs {
		d.Plugin = plugin
		s.dyn.defs[d.Key] = d
	}
	for _, mode := range modes {
		s.dyn.modes[mode] = plugin
	}
	return nil
}

// CheckPluginDefinitions is AddPluginDefinitions without adding: whether it
// would be accepted. For the install dialog, which has to say no before
// anything has been touched.
func (s *Service) CheckPluginDefinitions(plugin string, defs []Definition, modes []string) error {
	s.dyn.mu.RLock()
	defer s.dyn.mu.RUnlock()
	return s.checkPluginDefinitions(plugin, defs, modes)
}

// checkPluginDefinitions: the caller holds dyn.mu.
func (s *Service) checkPluginDefinitions(plugin string, defs []Definition, modes []string) error {
	for _, d := range defs {
		if d.Key == "" {
			return fmt.Errorf("settings: a definition with an empty key")
		}
		if _, taken := Defaults[d.Key]; taken {
			return fmt.Errorf("settings: %s is already defined", d.Key)
		}
		if other, taken := s.dyn.defs[d.Key]; taken && other.Plugin != plugin {
			return fmt.Errorf("settings: %s already belongs to plugin %s", d.Key, other.Plugin)
		}
	}
	for _, mode := range modes {
		if _, taken := pluginCaptchaModes[mode]; taken || coreCaptchaMode(mode) {
			return fmt.Errorf("settings: captcha mode %s already exists", mode)
		}
		if other, taken := s.dyn.modes[mode]; taken && other != plugin {
			return fmt.Errorf("settings: captcha mode %s already belongs to plugin %s", mode, other)
		}
	}
	return nil
}

// RemovePluginDefinitions forgets everything plugin added. The stored values
// stay where they are: whether they go is the uninstall's decision.
func (s *Service) RemovePluginDefinitions(plugin string) {
	s.dyn.mu.Lock()
	defer s.dyn.mu.Unlock()
	for key, d := range s.dyn.defs {
		if d.Plugin == plugin {
			delete(s.dyn.defs, key)
		}
	}
	for mode, owner := range s.dyn.modes {
		if owner == plugin {
			delete(s.dyn.modes, mode)
		}
	}
}

// definition is key's plugin definition, compiled in or installed, whatever
// its plugin's state.
func (s *Service) definition(key string) (Definition, bool) {
	if d, ok := definitions[key]; ok {
		return d, true
	}
	s.dyn.mu.RLock()
	defer s.dyn.mu.RUnlock()
	d, ok := s.dyn.defs[key]
	return d, ok
}

// LookupDefinition is Lookup for a service that has plugins installed at
// runtime: which grant owns a key, or whether it is a credential, does not
// change while its plugin is switched off.
func (s *Service) LookupDefinition(key string) (Definition, bool) { return s.definition(key) }

// AllDefinitions is every plugin setting this service knows, compiled in or
// installed, whatever state its plugin is in.
func (s *Service) AllDefinitions() []Definition {
	out := AllDefinitions()
	s.dyn.mu.RLock()
	defer s.dyn.mu.RUnlock()
	for _, d := range s.dyn.defs {
		out = append(out, d)
	}
	return out
}

// DefinitionsOf lists what one plugin defined, whatever its state.
func (s *Service) DefinitionsOf(plugin string) []Definition {
	out := DefinitionsOf(plugin)
	s.dyn.mu.RLock()
	for _, d := range s.dyn.defs {
		if d.Plugin == plugin {
			out = append(out, d)
		}
	}
	s.dyn.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// CaptchaModesOf lists the modes plugins added, compiled in or installed, by
// the plugin that owns each.
func (s *Service) CaptchaModesOf() map[string]string {
	out := CaptchaModes()
	s.dyn.mu.RLock()
	defer s.dyn.mu.RUnlock()
	for mode, plugin := range s.dyn.modes {
		out[mode] = plugin
	}
	return out
}

// defaultOf is key's default: a core or compiled-in one, or an installed
// plugin's.
func (s *Service) defaultOf(key string) string {
	if value, ok := Defaults[key]; ok {
		return value
	}
	s.dyn.mu.RLock()
	defer s.dyn.mu.RUnlock()
	return s.dyn.defs[key].Default
}

// OnChange registers fn to be told, after the fact, which keys were written
// or forgotten. It is how a plugin that derives something from its settings
// — the origins its browser half loads from — finds out to derive it again.
// fn runs on the writer's goroutine and must not write a setting itself.
func (s *Service) OnChange(fn func(keys []string)) {
	s.dyn.mu.Lock()
	s.listeners = append(s.listeners, fn)
	s.dyn.mu.Unlock()
}

func (s *Service) changed(keys []string) {
	s.dyn.mu.RLock()
	listeners := slices.Clone(s.listeners)
	s.dyn.mu.RUnlock()
	for _, fn := range listeners {
		fn(keys)
	}
}
