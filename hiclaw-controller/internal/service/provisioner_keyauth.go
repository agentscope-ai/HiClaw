package service

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// waitForKeyAuthSync waits for the Higress WASM key-auth plugin to sync
// after an AI route authorization change.
//
// Higress Console updates the key-auth WasmPlugin asynchronously after an
// AI route PUT. The WASM plugin typically reloads within 1-3 seconds, but
// can take longer on first boot when the plugin image is being pulled.
//
// We re-PUT the AI route to ensure the sync is triggered, then wait for
// the WASM plugin to reload. This is idempotent and safe to call multiple
// times. Works in both embedded and incluster modes.
func (p *Provisioner) waitForKeyAuthSync(ctx context.Context, consumerName string) {
	logger := log.FromContext(ctx)

	// Re-authorize to ensure the key-auth plugin sync is triggered.
	// This is a no-op if the consumer is already in allowedConsumers,
	// but the PUT itself triggers Higress Console to re-sync the
	// key-auth WasmPlugin's allow list and consumer credentials.
	if err := p.gateway.AuthorizeAIRoutes(ctx, consumerName); err != nil {
		logger.Error(err, "key-auth re-authorization failed (non-fatal)")
	}

	// Wait for WASM plugin to reload. The plugin image pull + config sync
	// typically takes 2-5 seconds on first boot, <1s on subsequent updates.
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}

	p.gateway.TriggerPush()
	logger.Info("key-auth sync wait completed", "consumer", consumerName)
}
