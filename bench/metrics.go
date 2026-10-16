package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type sampleSet struct {
	values []float64 // milliseconds
}

func (s *sampleSet) add(d time.Duration) {
	s.values = append(s.values, float64(d)/float64(time.Millisecond))
}

func (s *sampleSet) addMS(ms float64) {
	s.values = append(s.values, ms)
}

func (s sampleSet) len() int { return len(s.values) }

func (s sampleSet) percentile(p float64) float64 {
	if len(s.values) == 0 {
		return 0
	}
	cp := append([]float64(nil), s.values...)
	sort.Float64s(cp)
	if p <= 0 {
		return cp[0]
	}
	if p >= 100 {
		return cp[len(cp)-1]
	}
	idx := int(math.Ceil(p/100*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func (s sampleSet) median() float64 { return s.percentile(50) }

func (s sampleSet) min() float64 {
	if len(s.values) == 0 {
		return 0
	}
	m := s.values[0]
	for _, v := range s.values[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func (s sampleSet) max() float64 {
	if len(s.values) == 0 {
		return 0
	}
	m := s.values[0]
	for _, v := range s.values[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func loadDispatchFile(path string) (sampleSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sampleSet{}, nil
		}
		return sampleSet{}, err
	}
	var out sampleSet
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		us, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}
		out.addMS(float64(us) / 1000.0)
	}
	return out, nil
}

func fmtMS(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	if v >= 10 {
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

func medianOf(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]float64(nil), vals...)
	sort.Float64s(cp)
	return cp[len(cp)/2]
}

func spread(vals []float64) (lo, hi float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	lo, hi = vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}
