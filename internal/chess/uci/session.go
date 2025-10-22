package uci

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultReadyTimeout  = 4 * time.Second
	newGameRetryAttempts = 3
	newGameRetryDelay    = 150 * time.Millisecond
)

type Options struct {
	Threads    int
	SkillLevel int
	HashMB     int
	MultiPV    int
	Elo        int
}

type Limits struct {
	Depth          int
	MoveTimeMillis int
	NodeCap        int
}

type Candidate struct {
	Move      string
	EvalCP    int
	Principal []string
}

type Session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	search sync.Mutex
}

func NewSession(ctx context.Context, binaryPath string, opt Options) (*Session, error) {
	if err := validateOptions(opt); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, binaryPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdoutPipe.Close()
		return nil, fmt.Errorf("start engine: %w", err)
	}

	s := &Session{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
	}

	if err := s.initialize(ctx, opt); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

type SearchRequest struct {
	FEN         string
	Moves       []string
	Limits      Limits
	GoOverrides []string
}

type SearchResponse struct {
	Candidates []Candidate
	BestMove   string
}

func (s *Session) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	s.search.Lock()
	defer s.search.Unlock()

	positionCmd := buildPositionCommand(req.FEN, req.Moves)
	positionLog := strings.TrimSpace(positionCmd)
	if err := s.send(positionCmd); err != nil {
		return SearchResponse{}, fmt.Errorf("send position: %w", err)
	}

	goTokens := req.GoOverrides
	var err error
	if len(goTokens) == 0 {
		goTokens, err = buildGoTokens(req.Limits)
		if err != nil {
			return SearchResponse{}, err
		}
	}

	goCmd := strings.Join(goTokens, " ")
	if err := s.send(goCmd + "\n"); err != nil {
		return SearchResponse{}, fmt.Errorf("send go: %w", err)
	}

	deadline := computeSearchTimeout(req.Limits)
	searchCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	candidates := make(map[int]Candidate)
	var best string

	for {
		line, err := s.readLine(searchCtx)
		if err != nil {
			log.Printf("[uci] read error (position=%s, go=%s, moves=%v, limits=%+v): %v", positionLog, goCmd, req.Moves, req.Limits, err)
			return SearchResponse{}, fmt.Errorf("read line: %w", err)
		}
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "info "):
			if mv, cand, ok := parseInfo(line); ok {
				candidates[mv] = cand
			}
		case strings.HasPrefix(line, "bestmove"):
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				best = parts[1]
			}
			result := SearchResponse{Candidates: collapseCandidates(candidates), BestMove: best}
			return result, nil
		}
	}
}

func buildPositionCommand(fen string, moves []string) string {
	var sb strings.Builder
	if strings.TrimSpace(fen) == "" || fen == "startpos" {
		sb.WriteString("position startpos")
	} else {
		sb.WriteString("position fen ")
		sb.WriteString(fen)
	}
	if len(moves) > 0 {
		sb.WriteString(" moves ")
		sb.WriteString(strings.Join(moves, " "))
	}
	sb.WriteString("\n")
	return sb.String()
}

func validateOptions(opt Options) error {
	if opt.SkillLevel < 0 || opt.SkillLevel > 20 {
		return fmt.Errorf("skill level %d out of range 0-20", opt.SkillLevel)
	}
	if opt.HashMB <= 0 {
		return fmt.Errorf("hash size must be > 0: %d", opt.HashMB)
	}
	if opt.MultiPV <= 0 {
		return fmt.Errorf("multipv must be > 0: %d", opt.MultiPV)
	}
	if opt.Elo < 0 {
		return fmt.Errorf("elo must be >= 0: %d", opt.Elo)
	}
	return nil
}

func buildGoTokens(l Limits) ([]string, error) {
	args := []string{"go"}
	if l.Depth > 0 {
		args = append(args, "depth", strconv.Itoa(l.Depth))
	}
	if l.MoveTimeMillis > 0 {
		args = append(args, "movetime", strconv.Itoa(l.MoveTimeMillis))
	}
	if l.NodeCap > 0 {
		args = append(args, "nodes", strconv.Itoa(l.NodeCap))
	}
	if len(args) == 1 {
		return nil, fmt.Errorf("no search limits specified")
	}
	return args, nil
}

func computeSearchTimeout(l Limits) time.Duration {
	if l.MoveTimeMillis > 0 {
		ms := l.MoveTimeMillis + 2000
		return time.Duration(ms) * time.Millisecond * 3
	}
	if l.Depth > 0 {
		base := time.Duration(l.Depth) * 300 * time.Millisecond
		if base < 6*time.Second {
			base = 6 * time.Second
		}
		if base > 20*time.Second {
			base = 20 * time.Second
		}
		return base
	}
	return 6 * time.Second
}

func parseInfo(line string) (int, Candidate, bool) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return 0, Candidate{}, false
	}
	var (
		multipv = 1
		evalCP  int
		evalSet bool
		pvIdx   = -1
	)

	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "multipv":
			if i+1 < len(parts) {
				if v, err := strconv.Atoi(parts[i+1]); err == nil {
					multipv = v
				}
				i++
			}
		case "score":
			if i+2 < len(parts) {
				kind := parts[i+1]
				val := parts[i+2]
				switch kind {
				case "cp":
					if v, err := strconv.Atoi(val); err == nil {
						evalCP = v
						evalSet = true
					}
				case "mate":
					if v, err := strconv.Atoi(val); err == nil {
						const mateValue = 30000
						if v >= 0 {
							evalCP = mateValue
						} else {
							evalCP = -mateValue
						}
						evalSet = true
					}
				}
				i += 2
			}
		case "pv":
			pvIdx = i + 1
			i = len(parts)
		}
	}

	if pvIdx == -1 || pvIdx >= len(parts) {
		return 0, Candidate{}, false
	}
	principal := parts[pvIdx:]
	if len(principal) == 0 {
		return 0, Candidate{}, false
	}

	if !evalSet {
		evalCP = 0
	}

	cand := Candidate{
		Move:      principal[0],
		EvalCP:    evalCP,
		Principal: append([]string(nil), principal...),
	}
	return multipv, cand, true
}

func collapseCandidates(m map[int]Candidate) []Candidate {
	if len(m) == 0 {
		return nil
	}
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	result := make([]Candidate, 0, len(keys))
	for _, k := range keys {
		result = append(result, m[k])
	}
	return result
}

func (s *Session) EnsureReady(ctx context.Context) error {
	readyCtx, cancel := context.WithTimeout(ctx, defaultReadyTimeout)
	defer cancel()

	if err := s.send("isready\n"); err != nil {
		return fmt.Errorf("send isready: %w", err)
	}
	if err := s.awaitToken(readyCtx, "readyok"); err != nil {
		return fmt.Errorf("wait readyok: %w", err)
	}
	return nil
}

func (s *Session) NewGame(ctx context.Context) error {
	if err := s.send("ucinewgame\n"); err != nil {
		return fmt.Errorf("send ucinewgame: %w", err)
	}

	for attempt := 1; attempt <= newGameRetryAttempts; attempt++ {
		err := s.EnsureReady(ctx)
		if err == nil {
			return nil
		}
		if attempt == newGameRetryAttempts {
			return err
		}
		log.Printf("[uci] ensure ready retry %d/%d after ucinewgame: %v", attempt, newGameRetryAttempts, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(newGameRetryDelay):
		}
	}
	return nil
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stdin != nil {
		s.stdin.Close()
	}

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}

	if s.cmd != nil {
		return s.cmd.Wait()
	}
	return nil
}

func (s *Session) initialize(ctx context.Context, opt Options) error {
	initCtx, cancel := context.WithTimeout(ctx, defaultReadyTimeout)
	defer cancel()

	if err := s.send("uci\n"); err != nil {
		return fmt.Errorf("send uci: %w", err)
	}
	if err := s.awaitToken(initCtx, "uciok"); err != nil {
		return fmt.Errorf("wait uciok: %w", err)
	}

	if err := s.applyOptions(opt); err != nil {
		return err
	}

	if err := s.send("isready\n"); err != nil {
		return fmt.Errorf("send isready: %w", err)
	}
	if err := s.awaitToken(initCtx, "readyok"); err != nil {
		return fmt.Errorf("wait readyok: %w", err)
	}

	return nil
}

func (s *Session) applyOptions(opt Options) error {
	threadCount := opt.Threads
	if threadCount <= 0 {
		threadCount = 1
	}
	cmds := []string{
		fmt.Sprintf("setoption name Threads value %d\n", threadCount),
		fmt.Sprintf("setoption name Hash value %d\n", opt.HashMB),
		fmt.Sprintf("setoption name Skill Level value %d\n", opt.SkillLevel),
		fmt.Sprintf("setoption name MultiPV value %d\n", opt.MultiPV),
		"setoption name Minimum Thinking Time value 10\n",
		"setoption name Move Overhead value 100\n",
		"setoption name UCI_LimitStrength value true\n",
		fmt.Sprintf("setoption name UCI_Elo value %d\n", opt.Elo),
	}
	for _, cmd := range cmds {
		if err := s.send(cmd); err != nil {
			return fmt.Errorf("apply options: %w", err)
		}
	}
	return nil
}

func (s *Session) send(msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := io.WriteString(s.stdin, msg)
	return err
}

func (s *Session) awaitToken(ctx context.Context, token string) error {
	for {
		line, err := s.readLine(ctx)
		if err != nil {
			return err
		}
		if strings.Contains(line, token) {
			return nil
		}
	}
}

func (s *Session) readLine(ctx context.Context) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		line, err := s.stdout.ReadString('\n')
		ch <- result{line: strings.TrimSpace(line), err: err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		return res.line, res.err
	}
}
