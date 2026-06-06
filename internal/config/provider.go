package config

import "sync/atomic"

// Provider holds the current config behind an atomic pointer. A settings save
// builds a fresh snapshot and Stores it; readers Load the current snapshot
// without locking. The pointed-to Config is treated as immutable, so a reader
// never observes a torn field while a save is in flight.
type Provider struct {
	p atomic.Pointer[Config]
}

// NewProvider wraps an initial config.
func NewProvider(cfg *Config) *Provider {
	var pr Provider
	pr.p.Store(cfg)
	return &pr
}

// Current returns the live config snapshot. Treat it as read-only.
func (pr *Provider) Current() *Config { return pr.p.Load() }

// Store publishes a new config snapshot.
func (pr *Provider) Store(cfg *Config) { pr.p.Store(cfg) }
