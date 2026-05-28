package search

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const dims = 14
const topK = 5
const int16Scale = 32767
const numClusters = 256

type ReferenceData struct {
	Clusters [numClusters]cluster
	Count    int
}

type cluster struct {
	Vectors  []int16
	Frauds   []bool
	Centroid [dims]int32
	Count    int
}

func (rd *ReferenceData) Total() int {
	return rd.Count
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

func Preprocess(inputPath, outputPath string) error {
	records, err := loadJSON(inputPath)
	if err != nil {
		return err
	}

	rd := clusterKMeans(records)
	records = nil

	if err := rd.saveBinary(outputPath); err != nil {
		return err
	}
	return nil
}

func LoadBinary(path string) (*ReferenceData, error) {
	return loadBinary(path)
}

type rawRecord struct {
	vector [dims]int16
	fraud  bool
}

func loadJSON(path string) ([]rawRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
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
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		reader = gz
	} else {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek: %w", err)
		}
	}

	type ref struct {
		Vector []float64 `json:"vector"`
		Label  string    `json:"label"`
	}

	decoder := json.NewDecoder(reader)
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("array start: %w", err)
	}

	records := make([]rawRecord, 0, 3000000)
	for decoder.More() {
		var r ref
		if err := decoder.Decode(&r); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		var vec [dims]int16
		for i, v := range r.Vector {
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
		records = append(records, rawRecord{vector: vec, fraud: r.Label == "fraud"})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("array end: %w", err)
	}
	return records, nil
}

func clusterKMeans(records []rawRecord) *ReferenceData {
	n := len(records)
	rd := &ReferenceData{Count: n}

	centroids := make([][dims]int64, numClusters)
	step := n / numClusters
	if step < 1 {
		step = 1
	}
	for c := 0; c < numClusters; c++ {
		idx := c * step
		if idx >= n {
			idx = n - 1
		}
		for d := 0; d < dims; d++ {
			centroids[c][d] = int64(records[idx].vector[d])
		}
	}

	assignments := make([]int, n)

	for iter := 0; iter < 3; iter++ {
		for i := 0; i < n; i++ {
			bestDist := int64(1<<62 - 1)
			bestC := 0
			v := records[i].vector
			for c := 0; c < numClusters; c++ {
				var sumSq int64
				for d := 0; d < dims; d++ {
					diff := int64(v[d]) - centroids[c][d]
					sumSq += diff * diff
				}
				if sumSq < bestDist {
					bestDist = sumSq
					bestC = c
				}
			}
			assignments[i] = bestC
		}

		for c := 0; c < numClusters; c++ {
			for d := 0; d < dims; d++ {
				centroids[c][d] = 0
			}
		}
		counts := make([]int, numClusters)
		for i := 0; i < n; i++ {
			c := assignments[i]
			counts[c]++
			for d := 0; d < dims; d++ {
				centroids[c][d] += int64(records[i].vector[d])
			}
		}
		for c := 0; c < numClusters; c++ {
			if counts[c] > 0 {
				for d := 0; d < dims; d++ {
					centroids[c][d] = centroids[c][d] / int64(counts[c])
				}
			}
		}
	}

	clusterCounts := make([]int, numClusters)
	for i := 0; i < n; i++ {
		clusterCounts[assignments[i]]++
	}

	for c := 0; c < numClusters; c++ {
		rd.Clusters[c].Vectors = make([]int16, 0, clusterCounts[c]*dims)
		rd.Clusters[c].Frauds = make([]bool, 0, clusterCounts[c])
		for d := 0; d < dims; d++ {
			rd.Clusters[c].Centroid[d] = int32(centroids[c][d])
		}
	}

	for i := 0; i < n; i++ {
		c := assignments[i]
		rd.Clusters[c].Vectors = append(rd.Clusters[c].Vectors, records[i].vector[:]...)
		rd.Clusters[c].Frauds = append(rd.Clusters[c].Frauds, records[i].fraud)
		rd.Clusters[c].Count++
	}

	return rd
}

func (rd *ReferenceData) saveBinary(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 12)
	binary.LittleEndian.PutUint32(buf[0:4], 0x46524546)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(rd.Count))
	binary.LittleEndian.PutUint32(buf[8:12], numClusters)
	if _, err := f.Write(buf); err != nil {
		return err
	}

	for c := 0; c < numClusters; c++ {
		cl := &rd.Clusters[c]

		binary.LittleEndian.PutUint32(buf[0:4], uint32(cl.Count))
		if _, err := f.Write(buf[:4]); err != nil {
			return err
		}

		cent := make([]byte, dims*4)
		for d := 0; d < dims; d++ {
			binary.LittleEndian.PutUint32(cent[d*4:(d+1)*4], uint32(cl.Centroid[d]))
		}
		if _, err := f.Write(cent); err != nil {
			return err
		}

		if cl.Count == 0 {
			continue
		}

		vecBytes := make([]byte, cl.Count*dims*2)
		for i := 0; i < cl.Count*dims; i++ {
			binary.LittleEndian.PutUint16(vecBytes[i*2:(i+1)*2], uint16(cl.Vectors[i]))
		}
		if _, err := f.Write(vecBytes); err != nil {
			return err
		}

		fraudBytes := make([]byte, cl.Count)
		for i := 0; i < cl.Count; i++ {
			if cl.Frauds[i] {
				fraudBytes[i] = 1
			}
		}
		if _, err := f.Write(fraudBytes); err != nil {
			return err
		}
	}

	fmt.Printf("Saved %d vectors in %d clusters to %s\n", rd.Count, numClusters, path)
	return nil
}

func loadBinary(path string) (*ReferenceData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open binary: %w", err)
	}
	defer f.Close()

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if binary.LittleEndian.Uint32(header[0:4]) != 0x46524546 {
		return nil, fmt.Errorf("invalid magic")
	}

	rd := &ReferenceData{
		Count: int(binary.LittleEndian.Uint32(header[4:8])),
	}
	k := int(binary.LittleEndian.Uint32(header[8:12]))

	for c := 0; c < k; c++ {
		if _, err := io.ReadFull(f, header[:4]); err != nil {
			return nil, fmt.Errorf("read count: %w", err)
		}
		count := int(binary.LittleEndian.Uint32(header[:4]))

		cent := make([]byte, dims*4)
		if _, err := io.ReadFull(f, cent); err != nil {
			return nil, fmt.Errorf("read centroid: %w", err)
		}
		for d := 0; d < dims; d++ {
			rd.Clusters[c].Centroid[d] = int32(binary.LittleEndian.Uint32(cent[d*4 : (d+1)*4]))
		}

		if count == 0 {
			continue
		}

		rd.Clusters[c].Vectors = make([]int16, count*dims)
		vecBytes := make([]byte, count*dims*2)
		if _, err := io.ReadFull(f, vecBytes); err != nil {
			return nil, fmt.Errorf("read vectors: %w", err)
		}
		for i := 0; i < count*dims; i++ {
			rd.Clusters[c].Vectors[i] = int16(binary.LittleEndian.Uint16(vecBytes[i*2 : (i+1)*2]))
		}

		rd.Clusters[c].Frauds = make([]bool, count)
		fraudBytes := make([]byte, count)
		if _, err := io.ReadFull(f, fraudBytes); err != nil {
			return nil, fmt.Errorf("read fraud: %w", err)
		}
		for i := 0; i < count; i++ {
			rd.Clusters[c].Frauds[i] = fraudBytes[i] != 0
		}
		rd.Clusters[c].Count = count
	}

	fmt.Printf("Loaded %d vectors in %d clusters from binary\n", rd.Count, k)
	return rd, nil
}

func (rd *ReferenceData) FindNearest(query []float64) (fraudCount int, total int) {
	var query16 [dims]int16
	for i, v := range query {
		query16[i] = float64ToInt16(v)
	}

	type cd struct {
		idx  int
		dist int64
	}
	topClusters := make([]cd, 5)
	for i := range topClusters {
		topClusters[i] = cd{idx: -1, dist: 1<<62 - 1}
	}

	for c := 0; c < numClusters; c++ {
		if rd.Clusters[c].Count == 0 {
			continue
		}
		cent := rd.Clusters[c].Centroid
		var sumSq int64
		for d := 0; d < dims; d++ {
			diff := int64(query16[d]) - int64(cent[d])
			sumSq += diff * diff
		}

		j := 4
		for j > 0 && sumSq < topClusters[j-1].dist {
			topClusters[j] = topClusters[j-1]
			j--
		}
		if sumSq < topClusters[4].dist {
			topClusters[j] = cd{idx: c, dist: sumSq}
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

	q0, q1, q2, q3, q4, q5, q6, q7 := query16[0], query16[1], query16[2], query16[3], query16[4], query16[5], query16[6], query16[7]
	q8, q9, q10, q11, q12, q13 := query16[8], query16[9], query16[10], query16[11], query16[12], query16[13]

	for ci := 0; ci < 5; ci++ {
		c := topClusters[ci].idx
		if c < 0 {
			continue
		}
		cl := &rd.Clusters[c]
		vectors := cl.Vectors
		for idx := 0; idx < cl.Count; idx++ {
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
			bestSlot[j] = c
			bestIdx[j] = idx
		}
	}

	fraudCount = 0
	for i := 0; i < topK; i++ {
		if bestSlot[i] < 0 {
			continue
		}
		if rd.Clusters[bestSlot[i]].Frauds[bestIdx[i]] {
			fraudCount++
		}
	}

	return fraudCount, topK
}
