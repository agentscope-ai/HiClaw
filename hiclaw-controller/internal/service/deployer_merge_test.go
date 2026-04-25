package service

import (
	"encoding/json"
	"testing"
)

func TestMergeUserPluginConfig_PreservesUserDreamingConfig(t *testing.T) {
	generated := `{
		"plugins": {
			"load": { "paths": ["/opt/openclaw/extensions/matrix"] },
			"entries": {
				"matrix": { "enabled": true },
				"memory-core": {
					"enabled": true,
					"config": { "dreaming": { "enabled": true } }
				}
			}
		}
	}`
	existing := `{
		"plugins": {
			"load": { "paths": ["/opt/openclaw/extensions/matrix"] },
			"entries": {
				"matrix": { "enabled": true },
				"memory-core": {
					"enabled": true,
					"config": {
						"dreaming": {
							"enabled": true,
							"frequency": "0 */6 * * *",
							"timezone": "Asia/Shanghai"
						}
					}
				}
			}
		}
	}`

	merged, err := mergeUserPluginConfig([]byte(generated), []byte(existing))
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}

	plugins := result["plugins"].(map[string]interface{})
	entries := plugins["entries"].(map[string]interface{})
	mc := entries["memory-core"].(map[string]interface{})
	cfg := mc["config"].(map[string]interface{})
	dreaming := cfg["dreaming"].(map[string]interface{})

	if dreaming["frequency"] != "0 */6 * * *" {
		t.Errorf("user frequency lost: got %v", dreaming["frequency"])
	}
	if dreaming["timezone"] != "Asia/Shanghai" {
		t.Errorf("user timezone lost: got %v", dreaming["timezone"])
	}
	if dreaming["enabled"] != true {
		t.Errorf("dreaming.enabled should be true: got %v", dreaming["enabled"])
	}
}

func TestMergeUserPluginConfig_PreservesUserAddedPlugins(t *testing.T) {
	generated := `{
		"plugins": {
			"load": { "paths": ["/opt/openclaw/extensions/matrix"] },
			"entries": {
				"matrix": { "enabled": true },
				"memory-core": { "enabled": true }
			}
		}
	}`
	existing := `{
		"plugins": {
			"load": { "paths": ["/opt/openclaw/extensions/matrix", "/opt/openclaw/extensions/custom"] },
			"entries": {
				"matrix": { "enabled": true },
				"memory-core": { "enabled": true },
				"custom-plugin": { "enabled": true, "config": { "key": "value" } }
			}
		}
	}`

	merged, err := mergeUserPluginConfig([]byte(generated), []byte(existing))
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}

	plugins := result["plugins"].(map[string]interface{})
	entries := plugins["entries"].(map[string]interface{})

	if _, ok := entries["custom-plugin"]; !ok {
		t.Error("user-added custom-plugin was lost")
	}

	load := plugins["load"].(map[string]interface{})
	paths := load["paths"].([]interface{})
	pathSet := make(map[string]bool)
	for _, p := range paths {
		pathSet[p.(string)] = true
	}
	if !pathSet["/opt/openclaw/extensions/custom"] {
		t.Error("user-added extension path was lost")
	}
}

func TestMergeUserPluginConfig_AddsNewDefaultEntries(t *testing.T) {
	generated := `{
		"plugins": {
			"entries": {
				"matrix": { "enabled": true },
				"memory-core": { "enabled": true, "config": { "dreaming": { "enabled": true } } }
			}
		}
	}`
	// Existing config doesn't have memory-core (pre-upgrade)
	existing := `{
		"plugins": {
			"entries": {
				"matrix": { "enabled": true }
			}
		}
	}`

	merged, err := mergeUserPluginConfig([]byte(generated), []byte(existing))
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}

	plugins := result["plugins"].(map[string]interface{})
	entries := plugins["entries"].(map[string]interface{})

	mc, ok := entries["memory-core"]
	if !ok {
		t.Fatal("memory-core should be added from generated defaults")
	}
	mcMap := mc.(map[string]interface{})
	cfg := mcMap["config"].(map[string]interface{})
	dreaming := cfg["dreaming"].(map[string]interface{})
	if dreaming["enabled"] != true {
		t.Error("dreaming.enabled should be true for new default entry")
	}
}
