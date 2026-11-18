package plugin

import (
	"context"
	"fmt"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
)

// Refresh adopts package bytes and switches committed by another server.
// Migrations and audits already committed with those bytes on the writer.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// One SELECT binds every switch to the package checksum committed with it.
	// A later archive read also requires that checksum, so a concurrent update
	// can only defer this refresh to the next poll.
	rows, err := m.db.Query(ctx, `SELECT i.name,i.state,i.version,i.installed_at,i.installed_by,i.updated_at,i.updated_by,
		COALESCE(p.sha256,''),COALESCE(p.source,''),COALESCE(p.added_at,0),COALESCE(p.added_by,'')
		FROM plugin_installs i LEFT JOIN plugin_packages p ON p.name=i.name ORDER BY i.name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		name, sum, source, addedBy string
		addedAt                    int64
	}
	var items []item
	states := map[string]record{}
	present := map[string]bool{}
	for rows.Next() {
		var i item
		var state record
		if err := rows.Scan(&i.name, &state.State, &state.Version, &state.InstalledAt, &state.InstalledBy, &state.UpdatedAt, &state.UpdatedBy,
			&i.sum, &i.source, &i.addedAt, &i.addedBy); err != nil {
			return err
		}
		if state.State == stateRemoved {
			state.State = StateAvailable
		}
		states[i.name] = state
		if i.sum != "" {
			present[i.name] = true
			items = append(items, i)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for name, old := range *m.pkgs.Load() {
		if !present[name] {
			m.detachPackage(old)
		}
	}
	installed := map[string]bool{}
	for _, i := range items {
		if _, compiled := Lookup(i.name); compiled {
			continue
		}
		old := m.loadedPackage(i.name)
		if old != nil && old.pkg.SHA256 == i.sum {
			continue
		}
		var archive []byte
		if err := m.db.QueryRow(ctx, `SELECT archive FROM plugin_packages WHERE name = ? AND sha256 = ?`, i.name, i.sum).Scan(&archive); err != nil {
			return err
		}
		pkg, err := arcx.Parse(archive)
		if err != nil {
			return err
		}
		if pkg.SHA256 != i.sum || pkg.Manifest.Name != i.name {
			return fmt.Errorf("plugin: stored package %s does not match its manifest", i.name)
		}
		if err := m.checkPackage(pkg, old); err != nil {
			return err
		}
		l := &loaded{name: i.name, pkg: pkg, source: i.source, addedAt: i.addedAt, addedBy: i.addedBy}
		m.startBackend(l)
		m.swapPackages(func(pkgs map[string]*loaded) { pkgs[i.name] = l })
		if old != nil {
			m.dropRetiredSeams(old.pkg.Manifest, l.pkg.Manifest)
		}
		if err := m.attach(l); err != nil {
			m.restoreInstall(i.name, old, l)
			closeLater(l)
			return err
		}
		installed[i.name] = true
		closeLater(old)
	}
	for name, l := range *m.pkgs.Load() {
		if states[name].State == StateEnabled && (installed[name] || m.State(name) != StateEnabled) {
			m.refreshDescribe(l)
		}
	}
	m.states.Store(&states)
	m.users.RefreshFields()
	return nil
}
