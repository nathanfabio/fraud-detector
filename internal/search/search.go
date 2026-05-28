package search

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const dims = 14
const topK = 5
const int16Scale = 32767
const numParts = 16
const clustersPerPart = 128

type rawVec struct {
	data  [dims]int16
	fraud bool
}

type ReferenceData struct {
	Parts [numParts]partition
}

type partition struct {
	Clusters [clustersPerPart]cluster
	Count    int
}

type cluster struct {
	Vectors  []int16
	Frauds   []bool
	Count    int
	Centroid [dims]int32
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

	rawParts := make([][]rawVec, numParts)
	for i := 0; i < numParts; i++ {
		rawParts[i] = make([]rawVec, 0, 3000000/numParts)
	}

	for decoder.More() {
		var rec refRecord
		if err := decoder.Decode(&rec); err != nil {
			return nil, fmt.Errorf("decode record: %w", err)
		}

		var vec [dims]int16
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

		k := partKey(vec[:])
		rawParts[k] = append(rawParts[k], rawVec{data: vec, fraud: rec.Label == "fraud"})
	}

	_, err = decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("expected array end: %w", err)
	}

	rd := &ReferenceData{}
	totalCount := 0
	for p := 0; p < numParts; p++ {
		n := len(rawParts[p])
		rd.Parts[p].Count = n
		totalCount += n
		if n == 0 {
			continue
		}

		clusterVectors(rawParts[p], &rd.Parts[p])
		rawParts[p] = nil
	}
	fmt.Printf("Loaded %d reference vectors across %d partitions (clustered)\n", totalCount, numParts)

	return rd, nil
}

func clusterVectors(raw []rawVec, part *partition) {
	n := len(raw)
	if n == 0 {
		return
	}

	nClusters := clustersPerPart
	if n < nClusters {
		nClusters = n
	}

	centroids := make([][dims]int32, nClusters)
	assignments := make([]int, n)

	step := n / nClusters
	if step < 1 {
		step = 1
	}
	for c := 0; c < nClusters; c++ {
		idx := c * step
		if idx >= n {
			idx = n - 1
		}
		for d := 0; d < dims; d++ {
			centroids[c][d] = int32(raw[idx].data[d])
		}
	}

	for iter := 0; iter < 2; iter++ {
		for i := 0; i < n; i++ {
			bestDist := int64(1<<62 - 1)
			bestC := 0
			v := raw[i].data
			for c := 0; c < nClusters; c++ {
				var sumSq int64
				for d := 0; d < dims; d++ {
					diff := int64(v[d]) - int64(centroids[c][d])
					sumSq += diff * diff
				}
				if sumSq < bestDist {
					bestDist = sumSq
					bestC = c
				}
			}
			assignments[i] = bestC
		}

		for c := 0; c < nClusters; c++ {
			for d := 0; d < dims; d++ {
				centroids[c][d] = 0
			}
		}
		counts := make([]int, nClusters)
		for i := 0; i < n; i++ {
			c := assignments[i]
			counts[c]++
			for d := 0; d < dims; d++ {
				centroids[c][d] += int32(raw[i].data[d])
			}
		}
		for c := 0; c < nClusters; c++ {
			if counts[c] > 0 {
				for d := 0; d < dims; d++ {
					centroids[c][d] = centroids[c][d] / int32(counts[c])
				}
			}
		}
	}

	clusterCounts := make([]int, nClusters)
	for i := 0; i < n; i++ {
		clusterCounts[assignments[i]]++
	}

	for c := 0; c < nClusters; c++ {
		part.Clusters[c].Vectors = make([]int16, 0, clusterCounts[c]*dims)
		part.Clusters[c].Frauds = make([]bool, 0, clusterCounts[c])
		part.Clusters[c].Centroid = centroids[c]
	}

	for i := 0; i < n; i++ {
		c := assignments[i]
		part.Clusters[c].Vectors = append(part.Clusters[c].Vectors, raw[i].data[:]...)
		part.Clusters[c].Frauds = append(part.Clusters[c].Frauds, raw[i].fraud)
		part.Clusters[c].Count++
	}
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

	nClusters := clustersPerPart
	if part.Count < nClusters {
		nClusters = part.Count
	}

	type clusterDist struct {
		idx  int
		dist int64
	}

	searchClusters := 5
	if nClusters < searchClusters {
		searchClusters = nClusters
	}

	topClusters := make([]clusterDist, searchClusters)
	for i := range topClusters {
		topClusters[i] = clusterDist{idx: -1, dist: 1<<62 - 1}
	}

	for c := 0; c < nClusters; c++ {
		if part.Clusters[c].Count == 0 {
			continue
		}
		cent := part.Clusters[c].Centroid
		var sumSq int64
		for d := 0; d < dims; d++ {
			diff := int64(query16[d]) - int64(cent[d])
			sumSq += diff * diff
		}

		j := searchClusters - 1
		for j > 0 && sumSq < topClusters[j-1].dist {
			topClusters[j] = topClusters[j-1]
			j--
		}
		if sumSq < topClusters[searchClusters-1].dist {
			topClusters[j] = clusterDist{idx: c, dist: sumSq}
		}
	}

	var bestSlot [topK]int
	var bestIdx [topK]int
	var bestDist [topK]int64
	for i := 0; i < topK; i++ {
		bestDist[i] = 1<<62 - 1
		bestSlot[i] = -1
		bestIdx[i] = -1
	}

	for i := 0; i < searchClusters; i++ {
		c := topClusters[i].idx
		cl := &part.Clusters[c]
		if cl.Count == 0 {
			continue
		}
		searchCluster(query16[:], cl.Vectors, cl.Count, c, &bestSlot, &bestIdx, &bestDist)
	}

	fraudCount = 0
	for i := 0; i < topK; i++ {
		if bestSlot[i] < 0 {
			continue
		}
		if part.Clusters[bestSlot[i]].Frauds[bestIdx[i]] {
			fraudCount++
		}
	}

	return fraudCount, topK
}

func searchCluster(query []int16, vectors []int16, count int, clusterIdx int,
	bestSlot *[topK]int, bestIdx *[topK]int, bestDist *[topK]int64) {

	q0, q1, q2, q3, q4, q5, q6, q7 := query[0], query[1], query[2], query[3], query[4], query[5], query[6], query[7]
	q8, q9, q10, q11, q12, q13 := query[8], query[9], query[10], query[11], query[12], query[13]

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
			bestSlot[j] = bestSlot[j-1]
			bestIdx[j] = bestIdx[j-1]
			j--
		}
		bestDist[j] = sumSq
		bestSlot[j] = clusterIdx
		bestIdx[j] = idx
	}
}
