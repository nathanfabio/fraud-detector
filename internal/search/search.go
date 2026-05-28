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
const numPartitions = 16

type result struct {
	indices   [topK]int
	distances [topK]int64
}

type ReferenceData struct {
	Parts [numPartitions]partition
}

type partition struct {
	Vectors []int16
	Frauds  []bool
	Count   int
}

func (rd *ReferenceData) Total() int {
	t := 0
	for i := 0; i < numPartitions; i++ {
		t += rd.Parts[i].Count
	}
	return t
}

func partitionKey(vec []int16) int {
	key := 0
	if vec[5] == -1 {
		key |= 1 << 0
	}
	if vec[9] != 0 {
		key |= 1 << 1
	}
	if vec[10] != 0 {
		key |= 1 << 2
	}
	if vec[11] != 0 {
		key |= 1 << 3
	}
	return key
}

func queryKey(query []int16) int {
	return partitionKey(query)
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
	perPart := estimateCapacity / numPartitions
	rd := &ReferenceData{}
	for i := 0; i < numPartitions; i++ {
		rd.Parts[i].Vectors = make([]int16, 0, perPart*dims)
		rd.Parts[i].Frauds = make([]bool, 0, perPart)
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

		k := partitionKey(vec)
		rd.Parts[k].Vectors = append(rd.Parts[k].Vectors, vec...)
		rd.Parts[k].Frauds = append(rd.Parts[k].Frauds, rec.Label == "fraud")
		rd.Parts[k].Count++
	}

	_, err = decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("expected array end: %w", err)
	}

	totalCount := 0
	for i := 0; i < numPartitions; i++ {
		totalCount += rd.Parts[i].Count
	}
	fmt.Printf("Loaded %d reference vectors across %d partitions\n", totalCount, numPartitions)

	return rd, nil
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

	k := queryKey(query16)
	part := &rd.Parts[k]

	if part.Count < topK {
		return rd.searchAll(query16)
	}

	return rd.searchPartition(query16, part)
}

func (rd *ReferenceData) searchPartition(query16 []int16, part *partition) (fraudCount int, total int) {
	vectors := part.Vectors
	frauds := part.Frauds
	count := part.Count

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers < 1 {
		numWorkers = 1
	}
	chunkSize := (count + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	results := make([]result, numWorkers)
	for i := range results {
		for j := 0; j < topK; j++ {
			results[i].distances[j] = 1<<62 - 1
			results[i].indices[j] = -1
		}
	}

	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > count {
			end = count
		}
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(worker, start, end int) {
			defer wg.Done()
			best := &results[worker]

			for i := 0; i < topK; i++ {
				best.distances[i] = 1<<62 - 1
				best.indices[i] = -1
			}

			q0, q1, q2, q3, q4, q5, q6, q7 := query16[0], query16[1], query16[2], query16[3], query16[4], query16[5], query16[6], query16[7]
			q8, q9, q10, q11, q12, q13 := query16[8], query16[9], query16[10], query16[11], query16[12], query16[13]

			for idx := start; idx < end; idx++ {
				base := idx * dims

				worst := best.distances[topK-1]
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
				for j > 0 && sumSq < best.distances[j-1] {
					best.distances[j] = best.distances[j-1]
					best.indices[j] = best.indices[j-1]
					j--
				}
				best.distances[j] = sumSq
				best.indices[j] = idx
			}
		}(w, start, end)
	}

	wg.Wait()

	return mergeResults(results, frauds)
}

func (rd *ReferenceData) searchAll(query16 []int16) (fraudCount int, total int) {
	allVectors := make([]int16, 0, 3000000*dims)
	allFrauds := make([]bool, 0, 3000000)

	for i := 0; i < numPartitions; i++ {
		if rd.Parts[i].Count == 0 {
			continue
		}
		allVectors = append(allVectors, rd.Parts[i].Vectors...)
		allFrauds = append(allFrauds, rd.Parts[i].Frauds...)
	}

	p := &partition{
		Vectors: allVectors,
		Frauds:  allFrauds,
		Count:   len(allFrauds),
	}
	return rd.searchPartition(query16, p)
}

func mergeResults(results []result, frauds []bool) (fraudCount int, total int) {
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
		if finalBest[i] >= 0 && frauds[finalBest[i]] {
			fraudCount++
		}
	}

	return fraudCount, topK
}
