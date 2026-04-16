package loader

import "gopkg.in/yaml.v3"

// unmarshalYAML wraps yaml.v3 Unmarshal, allowing manager_impl.go
// to call it without a direct import of yaml.v3.
func unmarshalYAML(data []byte, out interface{}) error {
	return yaml.Unmarshal(data, out)
}
