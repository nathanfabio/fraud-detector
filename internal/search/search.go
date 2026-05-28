package search

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync/atomic"
)

const dims = 14
const topK = 5
const int16Scale = 32767
const numParts = 16
const windowSize = 50000

type ReferenceData struct {
	Parts     [numParts]partition
	useSorted atomic.Bool
}

type partition struct {
	Vectors []int16
	Frauds  []bool
	Count   int
	Sorted  []int
}

func (rd *ReferenceData) Total() int {
	t := 0
	for i := 0; i < numParts; i++ {
		t += rd.Parts[i].Count
	}
	return t
}

func partKey(vec []int16) int {
	k := 0
	if vec[5] == -1 {
		k |= 1
	}
	if vec[9] != 0 {
		k |= 2
	}
	if vec[10] != 0 {
		k |= 4
	}
	if vec[11] != 0 {
		k |= 8
	}
	return k
}

func LoadReferences(path string) (*ReferenceData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open references: %w", err)
	}
	defer f.Close()

	var reader io.Reader = f

	buf := make([]byte, 2)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	if buf[0] == 0x1f && buf[1] == 0x8b {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek: %w", err)
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	} else {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek: %w", err)
		}
	}

	type refRecord struct {
		Vector []float64 `json:"vector"`
		Label  string    `json:"label"`
	}

	decoder := json.NewDecoder(reader)

	_, err = decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("expected array start: %w", err)
	}

	rd := &ReferenceData{}
	for i := 0; i < numParts; i++ {
		rd.Parts[i].Vectors = make([]int16, 0, 3000000/numParts*dims)
		rd.Parts[i].Frauds = make([]bool, 0, 3000000/numParts)
	}

	for decoder.More() {
		var rec refRecord
		if err := decoder.Decode(&rec); err != nil {
			return nil, fmt.Errorf("decode record: %w", err)
		}

		vec := make([]int16, dims)
		for i, v := range rec.Vector {
			if v == -1 {
				vec[i] = -1
			} else {
				if v < 0 {
					v = 0
				}
				if v > 1 {
					v = 1
				}
				vec[i] = int16(v*int16Scale + 0.5)
			}
		}

		k := partKey(vec)
		rd.Parts[k].Vectors = append(rd.Parts[k].Vectors, vec...)
		rd.Parts[k].Frauds = append(rd.Parts[k].Frauds, rec.Label == "fraud")
		rd.Parts[k].Count++
	}

	_, err = decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("expected array end: %w", err)
	}

	totalCount := 0
	for i := 0; i < numParts; i++ {
		totalCount += rd.Parts[i].Count
	}
	fmt.Printf("Loaded %d reference vectors across %d partitions\n", totalCount, numParts)

	return rd, nil
}

func (rd *ReferenceData) BuildIndex() {
	for i := 0; i < numParts; i++ {
		if rd.Parts[i].Count > 0 {
			rd.Parts[i].Sorted = makeSortedIndex(rd.Parts[i].Vectors, rd.Parts[i].Count)
		}
	}
	rd.useSorted.Store(true)
}

func makeSortedIndex(vectors []int16, count int) []int {
	idx := make([]int, count)
	for i := 0; i < count; i++ {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		return vectors[idx[a]*dims] < vectors[idx[b]*dims]
	})
	return idx
}

func float64ToInt16(v float64) int16 {
	if v == -1 {
		return -1
	}
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return int16(v*int16Scale + 0.5)
}

func (rd *ReferenceData) FindNearest(query []float64) (fraudCount int, total int) {
	var query16 [dims]int16
	for i, v := range query {
		query16[i] = float64ToInt16(v)
	}

	p := partKey(query16[:])
	part := &rd.Parts[p]

	if part.Count < topK {
		return 0, topK
	}

	if rd.useSorted.Load() {
		return rd.findNearestSorted(query16[:], part)
	}
	return rd.findNearestFull(query16[:], part)
}

func (rd *ReferenceData) findNearestSorted(query16 []int16, part *partition) (fraudCount int, total int) {
	vectors := part.Vectors
	sorted := part.Sorted

	start := sort.Search(part.Count, func(i int) bool {
		return vectors[sorted[i]*dims] >= query16[0]
	})

	var bestSlot [topK]int
	var bestIdx [topK]int
	var bestDist [topK]int64
	for i := 0; i < topK; i++ {
		bestDist[i] = 1<<62 - 1
		bestSlot[i] = -1
		bestIdx[i] = -1
	}

	w := windowSize
	if w > part.Count {
		w = part.Count
	}
	lo := start - w/2
	if lo < 0 {
		lo = 0
	}
	hi := lo + w
	if hi > part.Count {
		hi = part.Count
		lo = hi - w
		if lo < 0 {
			lo = 0
		}
	}

	searchWindow(query16, vectors, sorted, lo, hi, &bestSlot, &bestIdx, &bestDist)

	fraudCount = 0
	for i := 0; i < topK; i++ {
		if bestSlot[i] < 0 {
			continue
		}
		if part.Frauds[bestIdx[i]] {
			fraudCount++
		}
	}
	return fraudCount, topK
}

func (rd *ReferenceData) findNearestFull(query16 []int16, part *partition) (fraudCount int, total int) {
	var bestIdx [topK]int
	var bestDist [topK]int64
	for i := 0; i < topK; i++ {
		bestDist[i] = 1<<62 - 1
		bestIdx[i] = -1
	}

	vectors := part.Vectors
	count := part.Count
	frauds := part.Frauds

	q0, q1, q2, q3, q4, q5, q6, q7 := query16[0], query16[1], query16[2], query16[3], query16[4], query16[5], query16[6], query16[7]
	q8, q9, q10, q11, q12, q13 := query16[8], query16[9], query16[10], query16[11], query16[12], query16[13]

	for idx := 0; idx < count; idx++ {
		base := idx * dims
		worst := bestDist[topK-1]
		var sumSq int64

		d := int64(q0) - int64(vectors[base])
		sumSq = d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q1) - int64(vectors[base+1])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q2) - int64(vectors[base+2])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q3) - int64(vectors[base+3])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q4) - int64(vectors[base+4])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q5) - int64(vectors[base+5])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q6) - int64(vectors[base+6])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q7) - int64(vectors[base+7])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q8) - int64(vectors[base+8])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q9) - int64(vectors[base+9])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q10) - int64(vectors[base+10])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q11) - int64(vectors[base+11])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q12) - int64(vectors[base+12])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q13) - int64(vectors[base+13])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}

		j := topK - 1
		for j > 0 && sumSq < bestDist[j-1] {
			bestDist[j] = bestDist[j-1]
			bestIdx[j] = bestIdx[j-1]
			j--
		}
		bestDist[j] = sumSq
		bestIdx[j] = idx
	}

	fraudCount = 0
	for i := 0; i < topK; i++ {
		if bestIdx[i] >= 0 && frauds[bestIdx[i]] {
			fraudCount++
		}
	}
	return fraudCount, topK
}

func searchWindow(query []int16, vectors []int16, sorted []int, lo, hi int,
	bestSlot *[topK]int, bestIdx *[topK]int, bestDist *[topK]int64) {

	q0, q1, q2, q3, q4, q5, q6, q7 := query[0], query[1], query[2], query[3], query[4], query[5], query[6], query[7]
	q8, q9, q10, q11, q12, q13 := query[8], query[9], query[10], query[11], query[12], query[13]

	for i := lo; i < hi; i++ {
		idx := sorted[i]
		base := idx * dims

		worst := bestDist[topK-1]
		var sumSq int64

		d := int64(q0) - int64(vectors[base])
		sumSq = d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q1) - int64(vectors[base+1])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q2) - int64(vectors[base+2])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q3) - int64(vectors[base+3])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q4) - int64(vectors[base+4])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q5) - int64(vectors[base+5])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q6) - int64(vectors[base+6])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q7) - int64(vectors[base+7])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q8) - int64(vectors[base+8])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q9) - int64(vectors[base+9])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q10) - int64(vectors[base+10])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q11) - int64(vectors[base+11])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q12) - int64(vectors[base+12])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}
		d = int64(q13) - int64(vectors[base+13])
		sumSq += d * d
		if sumSq >= worst {
			continue
		}

		j := topK - 1
		for j > 0 && sumSq < bestDist[j-1] {
			bestDist[j] = bestDist[j-1]
			bestSlot[j] = bestSlot[j-1]
			bestIdx[j] = bestIdx[j-1]
			j--
		}
		bestDist[j] = sumSq
		bestSlot[j] = 0
		bestIdx[j] = idx
	}
}
