package uci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
)

type PoolConfig struct {
	BinaryPath        string
	PerPresetCapacity int
}

type Pool struct {
	binaryPath        string
	perPresetCapacity int

	mu       sync.Mutex
	buckets  map[string]*sessionBucket
	sessions map[*Session]*sessionBucket
}

func NewPool(cfg PoolConfig) (*Pool, error) {
	if cfg.BinaryPath == "" {
		return nil, fmt.Errorf("binary path required")
	}
	if _, err := os.Stat(cfg.BinaryPath); err != nil {
		return nil, fmt.Errorf("stockfish binary check: %w", err)
	}

	capacity := cfg.PerPresetCapacity
	if capacity <= 0 {
		capacity = defaultPerPresetCapacity()
	}

	p := &Pool{
		binaryPath:        cfg.BinaryPath,
		perPresetCapacity: capacity,
		buckets:           make(map[string]*sessionBucket),
		sessions:          make(map[*Session]*sessionBucket),
	}
	return p, nil
}

func (p *Pool) Acquire(ctx context.Context, opt Options) (*Session, error) {
	key := optionsKey(opt)
	bucket := p.getBucket(key, opt)

	for {
		select {
		case session := <-bucket.idle:
			if session == nil {
				continue
			}
			if err := session.EnsureReady(ctx); err != nil {
				p.discard(session)
				continue
			}
			p.track(session, bucket)
			return session, nil
		default:
		}

		session, err := bucket.create(ctx)
		if err == nil {
			p.track(session, bucket)
			return session, nil
		}
		if !errors.Is(err, errBucketAtCapacity) {
			return nil, err
		}

		select {
		case session := <-bucket.idle:
			if session == nil {
				continue
			}
			if err := session.EnsureReady(ctx); err != nil {
				p.discard(session)
				continue
			}
			p.track(session, bucket)
			return session, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (p *Pool) Release(session *Session, err error) {
	if session == nil {
		return
	}

	p.mu.Lock()
	bucket, ok := p.sessions[session]
	if !ok {
		p.mu.Unlock()
		_ = session.Close()
		return
	}

	if err != nil {
		delete(p.sessions, session)
		p.mu.Unlock()
		bucket.discard(session)
		return
	}
	p.mu.Unlock()

	if !bucket.put(session) {
		p.mu.Lock()
		delete(p.sessions, session)
		p.mu.Unlock()
		bucket.discard(session)
	}
}

func (p *Pool) Close() error {
	p.mu.Lock()
	buckets := make([]*sessionBucket, 0, len(p.buckets))
	for _, b := range p.buckets {
		buckets = append(buckets, b)
	}
	p.sessions = make(map[*Session]*sessionBucket)
	p.mu.Unlock()

	var errs []error
	for _, bucket := range buckets {
		for {
			select {
			case session := <-bucket.idle:
				if session == nil {
					continue
				}
				if err := session.Close(); err != nil {
					errs = append(errs, err)
				}
				bucket.decrement()
			default:
				goto nextBucket
			}
		}
	nextBucket:
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (p *Pool) track(session *Session, bucket *sessionBucket) {
	p.mu.Lock()
	p.sessions[session] = bucket
	p.mu.Unlock()
}

func (p *Pool) discard(session *Session) {
	if session == nil {
		return
	}
	p.mu.Lock()
	bucket, ok := p.sessions[session]
	if ok {
		delete(p.sessions, session)
	}
	p.mu.Unlock()
	if ok {
		bucket.discard(session)
		return
	}
	_ = session.Close()
}

func (p *Pool) getBucket(key string, opt Options) *sessionBucket {
	p.mu.Lock()
	bucket, ok := p.buckets[key]
	if !ok {
		bucket = newSessionBucket(p.binaryPath, opt, p.perPresetCapacity)
		p.buckets[key] = bucket
	}
	p.mu.Unlock()
	return bucket
}

type sessionBucket struct {
	key        string
	opt        Options
	capacity   int
	binaryPath string

	mu    sync.Mutex
	total int
	idle  chan *Session
}

var errBucketAtCapacity = errors.New("session bucket at capacity")

func newSessionBucket(binaryPath string, opt Options, capacity int) *sessionBucket {
	if capacity <= 0 {
		capacity = 1
	}
	return &sessionBucket{
		key:        optionsKey(opt),
		opt:        opt,
		capacity:   capacity,
		binaryPath: binaryPath,
		idle:       make(chan *Session, capacity),
	}
}

func (b *sessionBucket) create(ctx context.Context) (*Session, error) {
	b.mu.Lock()
	if b.total >= b.capacity {
		b.mu.Unlock()
		return nil, errBucketAtCapacity
	}
	b.total++
	b.mu.Unlock()

	session, err := NewSession(ctx, b.binaryPath, b.opt)
	if err != nil {
		b.decrement()
		return nil, err
	}
	return session, nil
}

func (b *sessionBucket) put(session *Session) bool {
	select {
	case b.idle <- session:
		return true
	default:
		return false
	}
}

func (b *sessionBucket) discard(session *Session) {
	if session != nil {
		_ = session.Close()
	}
	b.decrement()
}

func (b *sessionBucket) decrement() {
	b.mu.Lock()
	if b.total > 0 {
		b.total--
	}
	b.mu.Unlock()
}

func optionsKey(opt Options) string {
	return fmt.Sprintf("thr=%d|skill=%d|hash=%d|multipv=%d|elo=%d",
		opt.Threads,
		opt.SkillLevel,
		opt.HashMB,
		opt.MultiPV,
		opt.Elo)
}

func defaultPerPresetCapacity() int {
	cpu := runtime.NumCPU()
	if cpu < 2 {
		return 2
	}
	if cpu > 4 {
		return 4
	}
	return cpu
}
