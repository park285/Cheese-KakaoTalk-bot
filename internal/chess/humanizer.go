package chess

import (
	"errors"
	"math"
	"math/rand"
)

type Candidate struct {
	Move      string
	EvalCP    int
	Principal []string
	Forced    bool
}

func SelectCandidate(p DifficultyPreset, candidates []Candidate, r *rand.Rand) (Candidate, bool, error) {
	if len(candidates) == 0 {
		return Candidate{}, false, errors.New("no candidates to choose from")
	}
	if err := ValidatePreset(p); err != nil {
		return Candidate{}, false, err
	}

	primaryLimit := p.PrimaryChoices
	if primaryLimit > len(candidates) {
		primaryLimit = len(candidates)
	}

	for i := 0; i < primaryLimit; i++ {
		if candidates[i].Forced {
			choice := candidates[i]
			if p.EvalNoise > 0 {
				offset := r.Intn(2*p.EvalNoise+1) - p.EvalNoise
				choice.EvalCP = saturatingAdd(choice.EvalCP, offset)
			}
			return choice, false, nil
		}
	}

	totalWeight := 0.0
	for i := 0; i < primaryLimit; i++ {
		totalWeight += p.CandidateWeights[i]
	}
	if totalWeight == 0 {
		return Candidate{}, false, errors.New("candidate weights sum to zero")
	}

	threshold := r.Float64() * totalWeight
	index := 0
	for i := 0; i < primaryLimit; i++ {
		threshold -= p.CandidateWeights[i]
		if threshold <= 0 {
			index = i
			break
		}
	}

	choice := candidates[index]

	if p.EvalNoise > 0 {
		offset := r.Intn(2*p.EvalNoise+1) - p.EvalNoise
		choice.EvalCP = saturatingAdd(choice.EvalCP, offset)
	}

	return choice, false, nil
}

func saturatingAdd(a, b int) int {
	sum := int64(a) + int64(b)
	if sum > math.MaxInt {
		return math.MaxInt
	}
	if sum < math.MinInt {
		return math.MinInt
	}
	return int(sum)
}
