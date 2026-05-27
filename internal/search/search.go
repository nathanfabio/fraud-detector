package search

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
)

const dims = 14
const topK = 5
const int16Scale = 32767

type ReferenceData struct {
	Vectors []int16
	Frauds  []bool
	Count   int
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

	estimateCapacity := 3000000
	vectors := make([]int16, 0, estimateCapacity*dims)
	frauds := make([]bool, 0, estimateCapacity)

	for decoder.More() {
		var rec refRecord
		if err := decoder.Decode(&rec); err != nil {
			return nil, fmt.Errorf("decode record: %w", err)
		}

		for _, v := range rec.Vector {
			if v == -1 {
				vectors = append(vectors, -1)
			} else {
				if v < 0 {
					v = 0
				}
				if v > 1 {
					v = 1
				}
				vectors = append(vectors, int16(v*int16Scale+0.5))
			}
		}

		frauds = append(frauds, rec.Label == "fraud")
	}

	_, err = decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("expected array end: %w", err)
	}

	count := len(frauds)
	fmt.Printf("Loaded %d reference vectors\n", count)

	return &ReferenceData{
		Vectors: vectors,
		Frauds:  frauds,
		Count:   count,
	}, nil
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
	query16 := make([]int16, dims)
	for i, v := range query {
		query16[i] = float64ToInt16(v)
	}

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers < 1 {
		numWorkers = 1
	}
	chunkSize := (rd.Count + numWorkers - 1) / numWorkers

	type result struct {
		indices   [topK]int
		distances [topK]int64
	}

	var wg sync.WaitGroup
	results := make([]result, numWorkers)

	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > rd.Count {
			end = rd.Count
		}
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(worker, start, end int) {
			defer wg.Done()
			best := results[worker]

			for i := 0; i < topK; i++ {
				best.distances[i] = 1<<62 - 1
				best.indices[i] = -1
			}

			refs := rd.Vectors
			for idx := start; idx < end; idx++ {
				base := idx * dims

				d0 := int64(query16[0]) - int64(refs[base])
				d1 := int64(query16[1]) - int64(refs[base+1])
				d2 := int64(query16[2]) - int64(refs[base+2])
				d3 := int64(query16[3]) - int64(refs[base+3])
				d4 := int64(query16[4]) - int64(refs[base+4])
				d5 := int64(query16[5]) - int64(refs[base+5])
				d6 := int64(query16[6]) - int64(refs[base+6])
				d7 := int64(query16[7]) - int64(refs[base+7])
				d8 := int64(query16[8]) - int64(refs[base+8])
				d9 := int64(query16[9]) - int64(refs[base+9])
				d10 := int64(query16[10]) - int64(refs[base+10])
				d11 := int64(query16[11]) - int64(refs[base+11])
				d12 := int64(query16[12]) - int64(refs[base+12])
				d13 := int64(query16[13]) - int64(refs[base+13])

				sumSq := d0*d0 + d1*d1 + d2*d2 + d3*d3 + d4*d4 +
					d5*d5 + d6*d6 + d7*d7 + d8*d8 + d9*d9 +
					d10*d10 + d11*d11 + d12*d12 + d13*d13

				if sumSq >= best.distances[topK-1] {
					continue
				}

				j := topK - 1
				for j > 0 && sumSq < best.distances[j-1] {
					best.distances[j] = best.distances[j-1]
					best.indices[j] = best.indices[j-1]
					j--
				}
				best.distances[j] = sumSq
				best.indices[j] = idx
			}

			results[worker] = best
		}(w, start, end)
	}

	wg.Wait()

	var finalBest [topK]int
	var finalDist [topK]int64
	for i := 0; i < topK; i++ {
		finalDist[i] = 1<<62 - 1
		finalBest[i] = -1
	}

	for _, r := range results {
		for i := 0; i < topK; i++ {
			if r.indices[i] == -1 {
				continue
			}
			d := r.distances[i]
			j := topK - 1
			for j > 0 && d < finalDist[j-1] {
				finalDist[j] = finalDist[j-1]
				finalBest[j] = finalBest[j-1]
				j--
			}
			if d < finalDist[topK-1] {
				finalDist[j] = d
				finalBest[j] = r.indices[i]
			}
		}
	}

	fraudCount = 0
	for i := 0; i < topK; i++ {
		if finalBest[i] >= 0 && rd.Frauds[finalBest[i]] {
			fraudCount++
		}
	}

	return fraudCount, topK
}
