package chess

import (
	"fmt"
	"strconv"
	"strings"
)

func BuildGoCommand(p DifficultyPreset) ([]string, error) {
	if err := ValidatePreset(p); err != nil {
		return nil, err
	}

	args := []string{"go"}
	if p.DepthCap > 0 {
		args = append(args, "depth", strconv.Itoa(p.DepthCap))
	}
	if p.MoveTimeMillis > 0 {
		args = append(args, "movetime", strconv.Itoa(p.MoveTimeMillis))
	}
	if p.NodeCap > 0 {
		args = append(args, "nodes", strconv.Itoa(p.NodeCap))
	}

	if len(args) == 1 {
		return nil, fmt.Errorf("preset %s does not define search limits", p.Name)
	}

	return args, nil
}

func FormatGoCommand(p DifficultyPreset) (string, error) {
	args, err := BuildGoCommand(p)
	if err != nil {
		return "", err
	}
	return strings.Join(args, " "), nil
}
