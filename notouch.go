package chantrace

import (
	"bytes"
	"context"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"
)

const (
	defaultNoTouchPollInterval        = 250 * time.Millisecond
	defaultNoTouchHistorySize         = 256
	defaultNoTouchBaselineSamples     = 12
	defaultNoTouchTriggerDelta        = 32
	defaultNoTouchTriggerConsecutive  = 3
	defaultNoTouchTriggerWindow       = 3 * time.Second
	defaultNoTouchCooldown            = 5 * time.Second
	defaultNoTouchBlockProfileRate    = 1_000_000
	defaultNoTouchMutexProfileRate    = 8
	defaultNoTouchProfileMaxBytes     = 16 << 10
	defaultNoTouchProfileSummaryLines = 40
)

// NoTouchOption configures no-touch runtime probing.
type NoTouchOption func(*NoTouchConfig)

// NoTouchConfig controls low-perturbation runtime probing.
type NoTouchConfig struct {
	// PollInterval controls passive sampling cadence.
	PollInterval time.Duration
	// HistorySize controls the retained sample count in report snapshots.
	HistorySize int
	// BaselineSamples controls how many initial samples are used to establish a baseline.
	BaselineSamples int
	// TriggerDelta is the goroutine-count delta above baseline required for anomaly tracking.
	TriggerDelta int
	// TriggerConsecutive is how many consecutive anomalous samples are needed to trigger profiling.
	TriggerConsecutive int
	// TriggerWindow controls how long block/mutex profiling is enabled per trigger.
	TriggerWindow time.Duration
	// Cooldown delays retriggering after a profiling window completes.
	Cooldown time.Duration
	// BlockProfileRate is passed to runtime.SetBlockProfileRate when a trigger starts.
	BlockProfileRate int
	// BlockProfileRestore is restored via runtime.SetBlockProfileRate when a trigger ends.
	BlockProfileRestore int
	// MutexProfileFraction is passed to runtime.SetMutexProfileFraction when a trigger starts.
	MutexProfileFraction int
	// ProfileMaxBytes caps profile summaries kept in the report.
	ProfileMaxBytes int
	// ProfileSummaryLines caps profile summary lines kept in the report.
	ProfileSummaryLines int
}

// NoTouchSample is a single passive runtime sample.
type NoTouchSample struct {
	Timestamp    int64  `json:"timestamp"`
	Goroutines   int    `json:"goroutines"`
	Delta        int    `json:"delta"`
	Baseline     int    `json:"baseline"`
	TriggerOn    bool   `json:"trigger_on"`
	TriggerCount uint64 `json:"trigger_count"`
}

// NoTouchSnapshot describes current no-touch probe state.
type NoTouchSnapshot struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`

	Timestamp int64 `json:"timestamp"`

	PollIntervalNS int64 `json:"poll_interval_ns"`

	Baseline          int `json:"baseline"`
	BaselineSamples   int `json:"baseline_samples"`
	CurrentGoroutines int `json:"current_goroutines"`
	CurrentDelta      int `json:"current_delta"`

	TriggerActive bool   `json:"trigger_active"`
	TriggerCount  uint64 `json:"trigger_count"`
	LastTriggerAt int64  `json:"last_trigger_at"`
	TriggerUntil  int64  `json:"trigger_until"`
	CooldownUntil int64  `json:"cooldown_until"`

	BlockProfileRate     int    `json:"block_profile_rate"`
	BlockProfileRestore  int    `json:"block_profile_restore"`
	MutexProfileFraction int    `json:"mutex_profile_fraction"`
	LastBlockProfile     string `json:"last_block_profile,omitempty"`
	LastBlockProfileAt   int64  `json:"last_block_profile_at,omitempty"`
	LastMutexProfile     string `json:"last_mutex_profile,omitempty"`
	LastMutexProfileAt   int64  `json:"last_mutex_profile_at,omitempty"`

	Samples []NoTouchSample `json:"samples,omitempty"`
}

// WithNoTouchPollInterval sets the passive sampling interval.
func WithNoTouchPollInterval(d time.Duration) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.PollInterval = d
	}
}

// WithNoTouchHistorySize sets retained sample count.
func WithNoTouchHistorySize(n int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.HistorySize = n
	}
}

// WithNoTouchBaselineSamples sets baseline warmup sample count.
func WithNoTouchBaselineSamples(n int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.BaselineSamples = n
	}
}

// WithNoTouchTriggerDelta sets goroutine-count delta threshold for anomaly tracking.
func WithNoTouchTriggerDelta(n int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.TriggerDelta = n
	}
}

// WithNoTouchTriggerConsecutive sets anomalous sample count required before triggering.
func WithNoTouchTriggerConsecutive(n int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.TriggerConsecutive = n
	}
}

// WithNoTouchTriggerWindow sets profiling window duration.
func WithNoTouchTriggerWindow(d time.Duration) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.TriggerWindow = d
	}
}

// WithNoTouchCooldown sets retrigger cooldown duration.
func WithNoTouchCooldown(d time.Duration) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.Cooldown = d
	}
}

// WithNoTouchBlockProfileRate sets runtime.SetBlockProfileRate value while triggered.
func WithNoTouchBlockProfileRate(rate int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.BlockProfileRate = rate
	}
}

// WithNoTouchBlockProfileRestore sets runtime.SetBlockProfileRate value after trigger stops.
func WithNoTouchBlockProfileRestore(rate int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.BlockProfileRestore = rate
	}
}

// WithNoTouchMutexProfileFraction sets runtime.SetMutexProfileFraction while triggered.
func WithNoTouchMutexProfileFraction(rate int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.MutexProfileFraction = rate
	}
}

// WithNoTouchProfileMaxBytes sets profile summary byte cap in report snapshots.
func WithNoTouchProfileMaxBytes(n int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.ProfileMaxBytes = n
	}
}

// WithNoTouchProfileSummaryLines sets profile summary line cap in report snapshots.
func WithNoTouchProfileSummaryLines(n int) NoTouchOption {
	return func(c *NoTouchConfig) {
		c.ProfileSummaryLines = n
	}
}

func defaultNoTouchConfig() NoTouchConfig {
	return NoTouchConfig{
		PollInterval:         defaultNoTouchPollInterval,
		HistorySize:          defaultNoTouchHistorySize,
		BaselineSamples:      defaultNoTouchBaselineSamples,
		TriggerDelta:         defaultNoTouchTriggerDelta,
		TriggerConsecutive:   defaultNoTouchTriggerConsecutive,
		TriggerWindow:        defaultNoTouchTriggerWindow,
		Cooldown:             defaultNoTouchCooldown,
		BlockProfileRate:     defaultNoTouchBlockProfileRate,
		BlockProfileRestore:  0,
		MutexProfileFraction: defaultNoTouchMutexProfileRate,
		ProfileMaxBytes:      defaultNoTouchProfileMaxBytes,
		ProfileSummaryLines:  defaultNoTouchProfileSummaryLines,
	}
}

func (c *NoTouchConfig) normalize() {
	if c.PollInterval <= 0 {
		c.PollInterval = defaultNoTouchPollInterval
	}
	if c.HistorySize <= 0 {
		c.HistorySize = defaultNoTouchHistorySize
	}
	if c.BaselineSamples <= 0 {
		c.BaselineSamples = 1
	}
	if c.TriggerDelta < 0 {
		c.TriggerDelta = 0
	}
	if c.TriggerConsecutive <= 0 {
		c.TriggerConsecutive = 1
	}
	if c.TriggerWindow <= 0 {
		c.TriggerWindow = defaultNoTouchTriggerWindow
	}
	if c.Cooldown < 0 {
		c.Cooldown = 0
	}
	if c.BlockProfileRate <= 0 {
		c.BlockProfileRate = defaultNoTouchBlockProfileRate
	}
	if c.BlockProfileRestore < 0 {
		c.BlockProfileRestore = 0
	}
	if c.MutexProfileFraction < 0 {
		c.MutexProfileFraction = 0
	}
	if c.ProfileMaxBytes <= 0 {
		c.ProfileMaxBytes = defaultNoTouchProfileMaxBytes
	}
	if c.ProfileSummaryLines <= 0 {
		c.ProfileSummaryLines = defaultNoTouchProfileSummaryLines
	}
}

var (
	noTouchMu      sync.RWMutex
	defaultNoTouch *noTouchProbe
)

type noTouchProbe struct {
	cfg    NoTouchConfig
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu sync.Mutex

	samples     []NoTouchSample
	sampleHead  int
	sampleCount int

	baselineSum   int
	baselineCount int
	baseline      int
	baselineReady bool

	currentGoroutines int
	currentDelta      int

	consecutiveAnomaly int

	triggerActive bool
	triggerUntil  time.Time
	cooldownUntil time.Time
	triggerCount  uint64
	lastTriggerAt time.Time

	prevMutexFraction int

	lastBlockProfile   string
	lastBlockProfileAt int64
	lastMutexProfile   string
	lastMutexProfileAt int64
}

func newNoTouchProbe(cfg NoTouchConfig) *noTouchProbe {
	cfg.normalize()
	ctx, cancel := context.WithCancel(context.Background())
	return &noTouchProbe{
		cfg:               cfg,
		ctx:               ctx,
		cancel:            cancel,
		done:              make(chan struct{}),
		samples:           make([]NoTouchSample, cfg.HistorySize),
		prevMutexFraction: -1,
	}
}

func (p *noTouchProbe) start() {
	go p.run()
}

func (p *noTouchProbe) stop() {
	p.cancel()
	<-p.done
}

func (p *noTouchProbe) run() {
	defer close(p.done)
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	p.tick(time.Now())
	for {
		select {
		case <-p.ctx.Done():
			p.shutdown()
			return
		case now := <-ticker.C:
			p.tick(now)
		}
	}
}

func (p *noTouchProbe) tick(now time.Time) {
	g := runtime.NumGoroutine()

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.baselineReady {
		p.baselineSum += g
		p.baselineCount++
		if p.baselineCount >= p.cfg.BaselineSamples {
			p.baseline = p.baselineSum / p.baselineCount
			p.baselineReady = true
		}
	}

	delta := 0
	if p.baselineReady {
		delta = g - p.baseline
	}
	p.currentGoroutines = g
	p.currentDelta = delta
	p.appendSampleLocked(NoTouchSample{
		Timestamp:    now.UnixNano(),
		Goroutines:   g,
		Delta:        delta,
		Baseline:     p.baseline,
		TriggerOn:    p.triggerActive,
		TriggerCount: p.triggerCount,
	})

	if p.triggerActive && !now.Before(p.triggerUntil) {
		p.stopTriggerLocked(now)
	}

	if p.triggerActive || !p.baselineReady || now.Before(p.cooldownUntil) {
		return
	}

	if delta >= p.cfg.TriggerDelta {
		p.consecutiveAnomaly++
	} else {
		p.consecutiveAnomaly = 0
	}

	if p.consecutiveAnomaly >= p.cfg.TriggerConsecutive {
		p.startTriggerLocked(now)
	}
}

func (p *noTouchProbe) shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.triggerActive {
		p.stopTriggerLocked(time.Now())
	}
}

func (p *noTouchProbe) startTriggerLocked(now time.Time) {
	p.triggerActive = true
	p.triggerCount++
	p.lastTriggerAt = now
	p.triggerUntil = now.Add(p.cfg.TriggerWindow)
	p.consecutiveAnomaly = 0

	p.prevMutexFraction = runtime.SetMutexProfileFraction(-1)
	runtime.SetMutexProfileFraction(p.cfg.MutexProfileFraction)
	runtime.SetBlockProfileRate(p.cfg.BlockProfileRate)
}

func (p *noTouchProbe) stopTriggerLocked(now time.Time) {
	p.lastBlockProfile = profileSummary("block", p.cfg.ProfileMaxBytes, p.cfg.ProfileSummaryLines)
	p.lastMutexProfile = profileSummary("mutex", p.cfg.ProfileMaxBytes, p.cfg.ProfileSummaryLines)
	p.lastBlockProfileAt = now.UnixNano()
	p.lastMutexProfileAt = now.UnixNano()

	runtime.SetBlockProfileRate(p.cfg.BlockProfileRestore)
	if p.prevMutexFraction >= 0 {
		runtime.SetMutexProfileFraction(p.prevMutexFraction)
	} else {
		runtime.SetMutexProfileFraction(0)
	}

	p.triggerActive = false
	p.triggerUntil = time.Time{}
	p.cooldownUntil = now.Add(p.cfg.Cooldown)
	p.consecutiveAnomaly = 0
}

func (p *noTouchProbe) appendSampleLocked(s NoTouchSample) {
	if len(p.samples) == 0 {
		return
	}
	if p.sampleCount < len(p.samples) {
		idx := (p.sampleHead + p.sampleCount) % len(p.samples)
		p.samples[idx] = s
		p.sampleCount++
		return
	}
	p.samples[p.sampleHead] = s
	p.sampleHead = (p.sampleHead + 1) % len(p.samples)
}

func (p *noTouchProbe) report() NoTouchSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	mode := "passive"
	if !p.baselineReady {
		mode = "warming-up"
	}
	if p.triggerActive {
		mode = "triggered"
	}

	out := NoTouchSnapshot{
		Enabled:              true,
		Mode:                 mode,
		Timestamp:            time.Now().UnixNano(),
		PollIntervalNS:       int64(p.cfg.PollInterval),
		Baseline:             p.baseline,
		BaselineSamples:      p.baselineCount,
		CurrentGoroutines:    p.currentGoroutines,
		CurrentDelta:         p.currentDelta,
		TriggerActive:        p.triggerActive,
		TriggerCount:         p.triggerCount,
		LastTriggerAt:        p.lastTriggerAt.UnixNano(),
		TriggerUntil:         p.triggerUntil.UnixNano(),
		CooldownUntil:        p.cooldownUntil.UnixNano(),
		BlockProfileRate:     p.cfg.BlockProfileRate,
		BlockProfileRestore:  p.cfg.BlockProfileRestore,
		MutexProfileFraction: p.cfg.MutexProfileFraction,
		LastBlockProfile:     p.lastBlockProfile,
		LastBlockProfileAt:   p.lastBlockProfileAt,
		LastMutexProfile:     p.lastMutexProfile,
		LastMutexProfileAt:   p.lastMutexProfileAt,
	}

	if p.sampleCount > 0 {
		out.Samples = make([]NoTouchSample, p.sampleCount)
		for i := range p.sampleCount {
			idx := (p.sampleHead + i) % len(p.samples)
			out.Samples[i] = p.samples[idx]
		}
	}

	return out
}

// NoTouchReport returns the current no-touch runtime probing snapshot.
func NoTouchReport() NoTouchSnapshot {
	noTouchMu.RLock()
	p := defaultNoTouch
	noTouchMu.RUnlock()
	if p == nil {
		return NoTouchSnapshot{
			Enabled:   false,
			Mode:      "disabled",
			Timestamp: time.Now().UnixNano(),
		}
	}
	return p.report()
}

func startNoTouchLocked(cfg NoTouchConfig) {
	stopNoTouchLocked()
	p := newNoTouchProbe(cfg)
	noTouchMu.Lock()
	defaultNoTouch = p
	noTouchMu.Unlock()
	p.start()
}

func stopNoTouchLocked() {
	noTouchMu.Lock()
	p := defaultNoTouch
	defaultNoTouch = nil
	noTouchMu.Unlock()
	if p != nil {
		p.stop()
	}
}

func profileSummary(name string, maxBytes, summaryLines int) string {
	prof := pprof.Lookup(name)
	if prof == nil {
		return ""
	}
	var b bytes.Buffer
	if err := prof.WriteTo(&b, 1); err != nil {
		return ""
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return s
	}
	if summaryLines > 0 {
		lines := strings.Split(s, "\n")
		if len(lines) > summaryLines {
			s = strings.Join(lines[:summaryLines], "\n")
		}
	}
	if maxBytes > 0 && len(s) > maxBytes {
		s = s[:maxBytes]
	}
	return s
}
