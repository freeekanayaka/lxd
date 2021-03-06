// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t profiles.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e profile names
//go:generate mapper stmt -p db -e profile names-by-Project
//go:generate mapper stmt -p db -e profile names-by-Project-and-Name
//go:generate mapper stmt -p db -e profile objects
//go:generate mapper stmt -p db -e profile objects-by-Project
//go:generate mapper stmt -p db -e profile objects-by-Project-and-Name
//go:generate mapper stmt -p db -e profile config-ref
//go:generate mapper stmt -p db -e profile config-ref-by-Project
//go:generate mapper stmt -p db -e profile config-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e profile devices-ref
//go:generate mapper stmt -p db -e profile devices-ref-by-Project
//go:generate mapper stmt -p db -e profile devices-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e profile used-by-ref
//go:generate mapper stmt -p db -e profile used-by-ref-by-Project
//go:generate mapper stmt -p db -e profile used-by-ref-by-Project-and-Name
//go:generate mapper stmt -p db -e profile id
//go:generate mapper stmt -p db -e profile create struct=Profile
//go:generate mapper stmt -p db -e profile create-config-ref
//go:generate mapper stmt -p db -e profile create-devices-ref
//go:generate mapper stmt -p db -e profile rename
//go:generate mapper stmt -p db -e profile delete
//go:generate mapper stmt -p db -e profile delete-config-ref
//go:generate mapper stmt -p db -e profile delete-devices-ref
//go:generate mapper stmt -p db -e profile update struct=Profile
//
//go:generate mapper method -p db -e profile URIs
//go:generate mapper method -p db -e profile List
//go:generate mapper method -p db -e profile Get
//go:generate mapper method -p db -e profile Exists struct=Profile
//go:generate mapper method -p db -e profile ID struct=Profile
//go:generate mapper method -p db -e profile ConfigRef
//go:generate mapper method -p db -e profile DevicesRef
//go:generate mapper method -p db -e profile UsedByRef
//go:generate mapper method -p db -e profile Create struct=Profile
//go:generate mapper method -p db -e profile Rename
//go:generate mapper method -p db -e profile Delete
//go:generate mapper method -p db -e profile Update struct=Profile

// Profile is a value object holding db-related details about a profile.
type Profile struct {
	ID          int
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string `db:"coalesce=''"`
	Config      map[string]string
	Devices     map[string]map[string]string
	UsedBy      []string
}

// ProfileToAPI is a convenience to convert a Profile db struct into
// an API profile struct.
func ProfileToAPI(profile *Profile) *api.Profile {
	p := &api.Profile{
		Name:   profile.Name,
		UsedBy: profile.UsedBy,
	}
	p.Description = profile.Description
	p.Config = profile.Config
	p.Devices = profile.Devices

	return p
}

// ProfileFilter can be used to filter results yielded by ProfileList.
type ProfileFilter struct {
	Project string
	Name    string
}

// GetProfileNames returns the names of all profiles in the given project.
func (c *Cluster) GetProfileNames(project string) ([]string, error) {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	q := fmt.Sprintf(`
SELECT profiles.name
 FROM profiles
 JOIN projects ON projects.id = profiles.project_id
WHERE projects.name = ?
`)
	inargs := []interface{}{project}
	var name string
	outfmt := []interface{}{name}
	result, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// GetProfile returns the profile with the given name.
func (c *Cluster) GetProfile(project, name string) (int64, *api.Profile, error) {
	var result *api.Profile
	var id int64

	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}

		profile, err := tx.GetProfile(project, name)
		if err != nil {
			return err
		}

		result = ProfileToAPI(profile)
		id = int64(profile.ID)

		return nil
	})
	if err != nil {
		return -1, nil, err
	}

	return id, result, nil
}

// GetProfiles returns the profiles with the given names in the given project.
func (c *Cluster) GetProfiles(project string, names []string) ([]api.Profile, error) {
	profiles := make([]api.Profile, len(names))

	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}

		for i, name := range names {
			profile, err := tx.GetProfile(project, name)
			if err != nil {
				return errors.Wrapf(err, "Load profile %q", name)
			}
			profiles[i] = *ProfileToAPI(profile)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return profiles, nil
}

// UpdateProfileDescription updates the description of the profile with the given ID.
func UpdateProfileDescription(tx *sql.Tx, id int64, description string) error {
	_, err := tx.Exec("UPDATE profiles SET description=? WHERE id=?", description, id)
	return err
}

// ClearProfileConfig resets the config of the profile with the given ID.
func ClearProfileConfig(tx *sql.Tx, id int64) error {
	_, err := tx.Exec("DELETE FROM profiles_config WHERE profile_id=?", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`DELETE FROM profiles_devices_config WHERE id IN
		(SELECT profiles_devices_config.id
		 FROM profiles_devices_config JOIN profiles_devices
		 ON profiles_devices_config.profile_device_id=profiles_devices.id
		 WHERE profiles_devices.profile_id=?)`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM profiles_devices WHERE profile_id=?", id)
	if err != nil {
		return err
	}
	return nil
}

// CreateProfileConfig adds a config to the profile with the given ID.
func CreateProfileConfig(tx *sql.Tx, id int64, config map[string]string) error {
	str := fmt.Sprintf("INSERT INTO profiles_config (profile_id, key, value) VALUES(?, ?, ?)")
	stmt, err := tx.Prepare(str)
	defer stmt.Close()
	if err != nil {
		return err
	}

	for k, v := range config {
		if v == "" {
			continue
		}

		_, err = stmt.Exec(id, k, v)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetInstancesWithProfile gets the names of the instance associated with the
// profile with the given name in the given project.
func (c *Cluster) GetInstancesWithProfile(project, profile string) (map[string][]string, error) {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	q := `SELECT instances.name, projects.name FROM instances
		JOIN instances_profiles ON instances.id == instances_profiles.instance_id
		JOIN projects ON projects.id == instances.project_id
		WHERE instances_profiles.profile_id ==
		  (SELECT profiles.id FROM profiles
		   JOIN projects ON projects.id == profiles.project_id
		   WHERE profiles.name=? AND projects.name=?)
		AND instances.type == 0`

	results := map[string][]string{}
	inargs := []interface{}{profile, project}
	var name string
	outfmt := []interface{}{name, name}

	output, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range output {
		if results[r[1].(string)] == nil {
			results[r[1].(string)] = []string{}
		}

		results[r[1].(string)] = append(results[r[1].(string)], r[0].(string))
	}

	return results, nil
}

// RemoveUnreferencedProfiles removes unreferenced profiles.
func (c *Cluster) RemoveUnreferencedProfiles() error {
	stmt := `
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
`
	err := exec(c, stmt)
	if err != nil {
		return err
	}

	return nil
}

// ExpandInstanceConfig expands the given instance config with the config
// values of the given profiles.
func ExpandInstanceConfig(config map[string]string, profiles []api.Profile) map[string]string {
	expandedConfig := map[string]string{}

	// Apply all the profiles
	profileConfigs := make([]map[string]string, len(profiles))
	for i, profile := range profiles {
		profileConfigs[i] = profile.Config
	}

	for i := range profileConfigs {
		for k, v := range profileConfigs[i] {
			expandedConfig[k] = v
		}
	}

	// Stick the given config on top
	for k, v := range config {
		expandedConfig[k] = v
	}

	return expandedConfig
}

// ExpandInstanceDevices expands the given instance devices with the devices
// defined in the given profiles.
func ExpandInstanceDevices(devices deviceConfig.Devices, profiles []api.Profile) deviceConfig.Devices {
	expandedDevices := deviceConfig.Devices{}

	// Apply all the profiles
	profileDevices := make([]deviceConfig.Devices, len(profiles))
	for i, profile := range profiles {
		profileDevices[i] = deviceConfig.NewDevices(profile.Devices)
	}
	for i := range profileDevices {
		for k, v := range profileDevices[i] {
			expandedDevices[k] = v
		}
	}

	// Stick the given devices on top
	for k, v := range devices {
		expandedDevices[k] = v
	}

	return expandedDevices
}
