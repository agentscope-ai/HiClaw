package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"sync"
)

// CallerIdentity represents the authenticated caller.
type CallerIdentity struct {
	Role       string // "manager" | "worker"
	WorkerName string // non-empty only when Role == "worker"
}

// KeyStore manages API keys for manager and workers.
type KeyStore struct {
	mu         sync.RWMutex
	managerKey string              // immutable after construction
	workerKeys map[string]string   // workerName -> apiKey
	keyIndex   map[string]string   // apiKey -> workerName (reverse index)
	persister  KeyPersister        // nil in local mode
}

// NewKeyStore creates a KeyStore with the given static manager key and optional persister.
func NewKeyStore(managerKey string, persister KeyPersister) *KeyStore {
	return &KeyStore{
		managerKey: managerKey,
		workerKeys: make(map[string]string),
		keyIndex:   make(map[string]string),
		persister:  persister,
	}
}

// AuthEnabled returns true if authentication is configured.
func (ks *KeyStore) AuthEnabled() bool {
	return ks.managerKey != ""
}

// Recover loads worker keys from the persister (called at startup).
func (ks *KeyStore) Recover(ctx context.Context) error {
	if ks.persister == nil {
		return nil
	}
	keys, err := ks.persister.Load(ctx)
	if err != nil {
		return err
	}

	ks.mu.Lock()
	defer ks.mu.Unlock()

	for name, key := range keys {
		ks.workerKeys[name] = key
		ks.keyIndex[key] = name
	}
	if len(keys) > 0 {
		log.Printf("[KeyStore] Recovered %d worker keys", len(keys))
	}
	return nil
}

// GenerateWorkerKey creates a cryptographically random API key for a worker.
func (ks *KeyStore) GenerateWorkerKey(workerName string) string {
	b := make([]byte, 32)
	rand.Read(b)
	key := hex.EncodeToString(b)

	ks.mu.Lock()
	if oldKey, exists := ks.workerKeys[workerName]; exists {
		delete(ks.keyIndex, oldKey)
	}
	ks.workerKeys[workerName] = key
	ks.keyIndex[key] = workerName
	snapshot := ks.snapshotLocked()
	ks.mu.Unlock()

	// persist outside lock: avoids blocking ValidateKey() readers during network I/O.
	// Trade-off: concurrent GenerateWorkerKey calls could persist stale snapshots,
	// but key ops are rare and in-memory state is always correct.
	ks.persist(snapshot)

	return key
}

// SetWorkerKey sets a known API key for a worker (used during recovery).
func (ks *KeyStore) SetWorkerKey(workerName, key string) {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	if oldKey, exists := ks.workerKeys[workerName]; exists {
		delete(ks.keyIndex, oldKey)
	}
	ks.workerKeys[workerName] = key
	ks.keyIndex[key] = workerName
}

// RemoveWorkerKey removes a worker's API key.
func (ks *KeyStore) RemoveWorkerKey(workerName string) {
	ks.mu.Lock()
	if key, exists := ks.workerKeys[workerName]; exists {
		delete(ks.keyIndex, key)
		delete(ks.workerKeys, workerName)
	}
	snapshot := ks.snapshotLocked()
	ks.mu.Unlock()

	ks.persist(snapshot)
}

// ValidateKey checks a key and returns the caller identity.
func (ks *KeyStore) ValidateKey(key string) (*CallerIdentity, bool) {
	if key == "" {
		return nil, false
	}

	// managerKey is immutable after construction, no lock needed
	if key == ks.managerKey {
		return &CallerIdentity{Role: RoleManager}, true
	}

	ks.mu.RLock()
	defer ks.mu.RUnlock()

	if workerName, exists := ks.keyIndex[key]; exists {
		return &CallerIdentity{Role: RoleWorker, WorkerName: workerName}, true
	}

	return nil, false
}

// snapshotLocked returns a copy of workerKeys. Must be called with mu held.
func (ks *KeyStore) snapshotLocked() map[string]string {
	cp := make(map[string]string, len(ks.workerKeys))
	for k, v := range ks.workerKeys {
		cp[k] = v
	}
	return cp
}

// persist saves the current keys to the persister (best-effort, logs on error).
func (ks *KeyStore) persist(keys map[string]string) {
	if ks.persister == nil {
		return
	}
	if err := ks.persister.Save(context.Background(), keys); err != nil {
		log.Printf("[KeyStore] WARNING: failed to persist keys: %v", err)
	}
}
