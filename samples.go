package chameleon

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type sampleRun struct {
	level byte
	count int
}

func sampleRuns(data []byte, invert bool) ([]sampleRun, int, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return nil, 0, fmt.Errorf("sample data must have 1..1048576 bytes")
	}
	sum := 0
	for _, b := range data {
		sum += int(b)
	}
	threshold := sum / len(data) / 2
	out := []sampleRun{}
	for _, b := range data {
		level := byte(0)
		if int(b) > threshold {
			level = 1
		}
		if invert {
			level ^= 1
		}
		if len(out) == 0 || out[len(out)-1].level != level {
			out = append(out, sampleRun{level, 1})
		} else {
			out[len(out)-1].count++
		}
	}
	return out, threshold, nil
}

type ModulationAnalysis struct {
	Samples            int     `json:"samples"`
	Min                byte    `json:"min"`
	Max                byte    `json:"max"`
	Mean               float64 `json:"mean"`
	Threshold          int     `json:"threshold"`
	Transitions        int     `json:"transitions"`
	HalfPeriodSamples  int     `json:"half_period_samples"`
	EstimatedRFDivider int     `json:"estimated_rf_divider"`
	Hint               string  `json:"heuristic_hint"`
}

func AnalyzeLFSamples(data []byte) (out ModulationAnalysis, err error) {
	runs, threshold, e := sampleRuns(data, false)
	if e != nil {
		return out, e
	}
	out.Samples = len(data)
	out.Min = 255
	sum := 0
	hist := map[int]int{}
	for _, b := range data {
		out.Min = min(out.Min, b)
		out.Max = max(out.Max, b)
		sum += int(b)
	}
	out.Mean = float64(sum) / float64(len(data))
	out.Threshold = threshold
	out.Transitions = len(runs) - 1
	if int(out.Max)-int(out.Min) < 32 {
		out.Hint = "flat carrier / no clear modulation"
		return out, nil
	}
	if len(runs) < 4 {
		out.Hint = "insufficient transitions"
		return out, nil
	}
	for _, r := range runs {
		hist[r.count]++
	}
	periods := []int{}
	for n := range hist {
		periods = append(periods, n)
	}
	sort.Slice(periods, func(i, j int) bool {
		if hist[periods[i]] == hist[periods[j]] {
			return periods[i] < periods[j]
		}
		return hist[periods[i]] > hist[periods[j]]
	})
	half := periods[0]
	out.HalfPeriodSamples = half
	best := 8
	for _, v := range []int{8, 16, 32, 40, 50, 64, 100, 128} {
		if math.Abs(float64(v-2*half)) < math.Abs(float64(best-2*half)) {
			best = v
		}
	}
	out.EstimatedRFDivider = best
	long := 0
	for _, r := range runs {
		if r.count > 3*half {
			long++
		}
	}
	out.Hint = "FSK or mixed (heuristic only)"
	if float64(long) > float64(len(runs))*.3 {
		out.Hint = "ASK/NRZ (heuristic only)"
	} else if len(periods) > 1 && math.Abs(float64(periods[1]-2*half)) <= float64(max(2, half/3)) {
		out.Hint = "Manchester (heuristic only)"
	} else if len(periods) <= 4 {
		out.Hint = "biphase (heuristic only)"
	}
	return out, nil
}

type ManchesterResult struct {
	Bits         string `json:"bits"`
	Clock        int    `json:"rf_divider"`
	Threshold    int    `json:"threshold"`
	Phase        int    `json:"phase"`
	TimingErrors int    `json:"timing_errors"`
	InvalidPairs int    `json:"invalid_pairs"`
}

func DecodeManchester(data []byte, clock int, invert bool, phase int) (out ManchesterResult, err error) {
	if clock < 2 || clock > 1024 || clock%2 != 0 || phase < 0 || phase > 1 {
		return out, fmt.Errorf("clock must be even 2..1024; phase 0/1")
	}
	runs, threshold, e := sampleRuns(data, invert)
	if e != nil {
		return out, e
	}
	out.Clock, out.Threshold, out.Phase = clock, threshold, phase
	half := clock / 2
	levels := []byte{}
	for _, r := range runs {
		n := int(math.Round(float64(r.count) / float64(half)))
		if n < 1 || n > 2 || int(math.Abs(float64(r.count-n*half))) > max(1, half/3) {
			out.TimingErrors++
			levels = append(levels, 2)
			continue
		}
		for i := 0; i < n; i++ {
			levels = append(levels, r.level)
		}
	}
	var bits strings.Builder
	for i := phase; i+1 < len(levels); i += 2 {
		if levels[i] > 1 || levels[i+1] > 1 || levels[i] == levels[i+1] {
			out.InvalidPairs++
			continue
		}
		if levels[i] == 0 {
			bits.WriteByte('0')
		} else {
			bits.WriteByte('1')
		}
	}
	out.Bits = bits.String()
	return out, nil
}
func LFSamplesCSV(data []byte, rateHz int) (string, error) {
	if _, _, e := sampleRuns(data, false); e != nil {
		return "", e
	}
	if rateHz < 1 || rateHz > 10000000 {
		return "", fmt.Errorf("invalid sample rate")
	}
	var b strings.Builder
	b.WriteString("index,time_us,value\n")
	for i, v := range data {
		fmt.Fprintf(&b, "%d,%.6f,%d\n", i, float64(i)*1e6/float64(rateHz), v)
	}
	return b.String(), nil
}

// LFSamplesSVG exports a bounded min/max envelope, retaining narrow spikes.
// It is a static file alternative to the Python desktop plotting window.
func LFSamplesSVG(data []byte) (string, error) {
	if _, _, e := sampleRuns(data, false); e != nil {
		return "", e
	}
	var points strings.Builder
	buckets := min(1024, len(data))
	for i := 0; i < buckets; i++ {
		start, end := i*len(data)/buckets, (i+1)*len(data)/buckets
		lo, hi := byte(255), byte(0)
		for _, v := range data[start:end] {
			lo = min(lo, v)
			hi = max(hi, v)
		}
		x := 40 + float64(i)*960/float64(max(1, buckets-1))
		fmt.Fprintf(&points, "%.2f,%.2f %.2f,%.2f ", x, 280-float64(lo), x, 280-float64(hi))
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1040 320"><rect width="1040" height="320" fill="white"/><path d="M40 20V280H1010" fill="none" stroke="black"/><polyline points="%s" fill="none" stroke="#176caa" stroke-width="1"/><text x="40" y="310" font-size="14">LF samples: %d (8-bit ADC)</text></svg>`, points.String(), len(data)), nil
}
