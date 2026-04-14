package service

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var wasmPluginGVR = schema.GroupVersionResource{
	Group:    "extensions.higress.io",
	Version:  "v1alpha1",
	Resource: "wasmplugins",
}

const keyAuthPluginName = "key-auth.internal"

// syncKeyAuthAllowList patches the key-auth WasmPlugin to ensure the consumer
// is in the per-route allow list for all AI route matchRules.
//
// Higress Console's PUT /v1/ai/routes API updates authConfig.allowedConsumers
// in the AI route ConfigMap, but may not reliably sync the key-auth WasmPlugin's
// per-route allow list. This method patches the WasmPlugin directly as a fallback.
func (p *Provisioner) syncKeyAuthAllowList(ctx context.Context, consumerName string) error {
	if p.restConfig == nil {
		return nil // no K8s access (e.g. unit tests)
	}

	logger := log.FromContext(ctx)

	dynClient, err := dynamic.NewForConfig(p.restConfig)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	obj, err := dynClient.Resource(wasmPluginGVR).Namespace(p.namespace).Get(ctx, keyAuthPluginName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get key-auth WasmPlugin: %w", err)
	}

	matchRules, found, err := unstructured.NestedSlice(obj.Object, "spec", "matchRules")
	if err != nil || !found {
		return fmt.Errorf("read matchRules: found=%v err=%v", found, err)
	}

	changed := false
	for i, rule := range matchRules {
		ruleMap, ok := rule.(map[string]interface{})
		if !ok {
			continue
		}

		// Skip disabled rules
		if disabled, _, _ := unstructured.NestedBool(ruleMap, "configDisable"); disabled {
			continue
		}

		// Only patch rules that target AI routes (ingress contains "ai-route-")
		ingress, _, _ := unstructured.NestedStringSlice(ruleMap, "ingress")
		isAIRoute := false
		for _, ing := range ingress {
			if len(ing) >= 9 && ing[:9] == "ai-route-" {
				isAIRoute = true
				break
			}
		}
		if !isAIRoute {
			continue
		}

		// Get current allow list
		config, _, _ := unstructured.NestedMap(ruleMap, "config")
		if config == nil {
			config = map[string]interface{}{}
		}

		allowRaw, _ := config["allow"]
		var allow []string
		if arr, ok := allowRaw.([]interface{}); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					allow = append(allow, s)
				}
			}
		}

		// Check if consumer is already in allow list
		found := false
		for _, a := range allow {
			if a == consumerName {
				found = true
				break
			}
		}
		if found {
			continue
		}

		// Add consumer to allow list
		allow = append(allow, consumerName)
		allowIface := make([]interface{}, len(allow))
		for j, a := range allow {
			allowIface[j] = a
		}
		config["allow"] = allowIface
		ruleMap["config"] = config
		matchRules[i] = ruleMap
		changed = true
	}

	if !changed {
		logger.Info("key-auth allow list already up-to-date", "consumer", consumerName)
		return nil
	}

	// Build a strategic merge patch
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"matchRules": matchRules,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	_, err = dynClient.Resource(wasmPluginGVR).Namespace(p.namespace).Patch(
		ctx, keyAuthPluginName, types.MergePatchType, patchBytes, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch key-auth WasmPlugin: %w", err)
	}

	logger.Info("patched key-auth WasmPlugin allow list", "consumer", consumerName)
	return nil
}
