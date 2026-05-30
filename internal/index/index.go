package index

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"unsafe"
)

const dims = 14
const topK = 5
const numClusters = 1000
const nprobe = 2
const maxScanPerCluster = 1000
const ivfMagic = 0x00415649

type Vector [dims]int8

type IVFIndex struct {
	Vectors    []Vector
	Labels     []uint8
	Centroids  []Vector
	Offsets    []int
	NumClusters int
}

func quantize(v float64) int8 {
	if v == -1 {
		return -1
	}
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return int8(math.Round(v * 127.0))
}

type rawRecord struct {
	vec   Vector
	label uint8
}

func BuildIndex(inputPath, outputPath string) error {
	records, err := loadJSON(inputPath)
	if err != nil {
		return err
	}
	fmt.Printf("Loaded %d records for index building\n", len(records))

	idx := buildIVF(records)
	records = nil

	if err := idx.saveBinary(outputPath); err != nil {
		return err
	}
	return nil
}

func LoadBinary(path string) (*IVFIndex, error) {
	return loadBinary(path)
}

func loadJSON(path string) ([]rawRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var reader io.Reader = f
	buf := make([]byte, 2)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, err
	}
	if buf[0] == 0x1f && buf[1] == 0x8b {
		f.Seek(0, io.SeekStart)
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	} else {
		f.Seek(0, io.SeekStart)
	}

	type ref struct {
		Vector []float64 `json:"vector"`
		Label  string    `json:"label"`
	}
	decoder := json.NewDecoder(reader)
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}

	records := make([]rawRecord, 0, 3000000)
	for decoder.More() {
		var r ref
		if err := decoder.Decode(&r); err != nil {
			return nil, err
		}
		var vec Vector
		for i, v := range r.Vector {
			vec[i] = quantize(v)
		}
		label := uint8(0)
		if r.Label == "fraud" {
			label = 1
		}
		records = append(records, rawRecord{vec, label})
	}
	decoder.Token()
	return records, nil
}

func buildIVF(records []rawRecord) *IVFIndex {
	n := len(records)
	rng := rand.New(rand.NewSource(42))

	centroids := make([]Vector, numClusters)
	kpp := numClusters
	if kpp > 100 {
		kpp = 100
	}
	if kpp > n {
		kpp = n
	}
	sampleSize := n / 20
	if sampleSize < 150000 {
		sampleSize = n
		if sampleSize > 150000 {
			sampleSize = 150000
		}
	}

	for i := 0; i < kpp; i++ {
		if i == 0 {
			centroids[i] = records[rng.Intn(n)].vec
		} else {
			bestDist := int64(-1)
			bestIdx := 0
			for j := 0; j < sampleSize && j < n; j++ {
				minDist := int64(1<<63 - 1)
				for k := 0; k < i; k++ {
					d := int64(manhattanDist(records[j].vec, centroids[k]))
					if d < minDist {
						minDist = d
					}
				}
				if minDist > bestDist {
					bestDist = minDist
					bestIdx = j
				}
			}
			centroids[i] = records[bestIdx].vec
		}
	}

	for i := kpp; i < numClusters && i < n; i++ {
		centroids[i] = records[rng.Intn(n)].vec
	}

	for iter := 0; iter < 10; iter++ {
		batchSize := n / 5
		if batchSize < 100000 {
			batchSize = n
		}
		perm := make([]int, batchSize)
		for i := 0; i < batchSize; i++ {
			perm[i] = rng.Intn(n)
		}

		acc := make([][dims]float64, numClusters)
		counts := make([]int, numClusters)

		for _, idx := range perm {
			v := records[idx].vec
			bestC := 0
			bestDist := int32(1<<31 - 1)
			for c := 0; c < numClusters; c++ {
				d := manhattanDist(v, centroids[c])
				if d < bestDist {
					bestDist = d
					bestC = c
				}
			}
			counts[bestC]++
			for d := 0; d < dims; d++ {
				acc[bestC][d] += float64(v[d])
			}
		}

		for c := 0; c < numClusters; c++ {
			if counts[c] > 0 {
				cnt := float64(counts[c])
				for d := 0; d < dims; d++ {
					centroids[c][d] = int8(math.Round(acc[c][d] / cnt))
				}
			}
		}
	}

	assignments := make([]int, n)
	clusterCounts := make([]int, numClusters)
	for i := 0; i < n; i++ {
		bestC := 0
		bestDist := int32(1<<31 - 1)
		for c := 0; c < numClusters; c++ {
			d := manhattanDist(records[i].vec, centroids[c])
			if d < bestDist {
				bestDist = d
				bestC = c
			}
		}
		assignments[i] = bestC
		clusterCounts[bestC]++
	}

	byCluster := make([][]int, numClusters)
	for c := 0; c < numClusters; c++ {
		byCluster[c] = make([]int, 0, clusterCounts[c])
	}
	for i := 0; i < n; i++ {
		byCluster[assignments[i]] = append(byCluster[assignments[i]], i)
	}

	idx := &IVFIndex{
		Vectors:     make([]Vector, n),
		Labels:      make([]uint8, n),
		Centroids:   centroids,
		Offsets:     make([]int, numClusters+1),
		NumClusters: numClusters,
	}

	pos := 0
	for c := 0; c < numClusters; c++ {
		idx.Offsets[c] = pos
		for _, origIdx := range byCluster[c] {
			idx.Vectors[pos] = records[origIdx].vec
			idx.Labels[pos] = records[origIdx].label
			pos++
		}
	}
	idx.Offsets[numClusters] = n

	return idx
}

func (idx *IVFIndex) Search(query *Vector) int {
	var bestClusters [nprobe]struct {
		c    int
		dist int32
	}
	for i := 0; i < nprobe; i++ {
		bestClusters[i] = struct {
			c    int
			dist int32
		}{-1, 1<<31 - 1}
	}

	for c := 0; c < idx.NumClusters; c++ {
		d := manhattanDist(*query, idx.Centroids[c])
		j := nprobe - 1
		for j > 0 && d < bestClusters[j-1].dist {
			bestClusters[j] = bestClusters[j-1]
			j--
		}
		if d < bestClusters[nprobe-1].dist {
			bestClusters[j] = struct {
				c    int
				dist int32
			}{c, d}
		}
	}

	var bestDist [topK]int32
	var bestLabels [topK]uint8
	for i := 0; i < topK; i++ {
		bestDist[i] = 1<<31 - 1
	}

	for ci := 0; ci < nprobe; ci++ {
		c := bestClusters[ci].c
		if c < 0 {
			continue
		}
		start := idx.Offsets[c]
		end := idx.Offsets[c+1]
		count := end - start
		if count > maxScanPerCluster {
			count = maxScanPerCluster
			end = start + count
		}

		for i := start; i < end; i++ {
			d := manhattanDist(*query, idx.Vectors[i])
			if d >= bestDist[topK-1] {
				continue
			}
			j := topK - 1
			for j > 0 && d < bestDist[j-1] {
				bestDist[j] = bestDist[j-1]
				bestLabels[j] = bestLabels[j-1]
				j--
			}
			bestDist[j] = d
			bestLabels[j] = idx.Labels[i]
		}
	}

	fraudCount := 0
	for i := 0; i < topK; i++ {
		if bestLabels[i] == 1 {
			fraudCount++
		}
	}
	return fraudCount
}

func manhattanDist(a, b Vector) int32 {
	var sum int32
	for i := 0; i < dims; i++ {
		da := int32(a[i]) - int32(b[i])
		if da < 0 {
			sum -= da
		} else {
			sum += da
		}
	}
	return sum
}

func (idx *IVFIndex) saveBinary(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	b := make([]byte, 12)
	binary.LittleEndian.PutUint32(b[0:4], ivfMagic)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(idx.Vectors)))
	binary.LittleEndian.PutUint32(b[8:12], uint32(idx.NumClusters))
	if _, err := f.Write(b); err != nil {
		return err
	}

	for _, v := range idx.Vectors {
		raw := unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), dims)
		if _, err := f.Write(raw); err != nil {
			return err
		}
	}
	if _, err := f.Write(idx.Labels); err != nil {
		return err
	}
	for _, c := range idx.Centroids {
		raw := unsafe.Slice((*byte)(unsafe.Pointer(&c[0])), dims)
		if _, err := f.Write(raw); err != nil {
			return err
		}
	}
	offBytes := make([]byte, (idx.NumClusters+1)*4)
	for i, o := range idx.Offsets {
		binary.LittleEndian.PutUint32(offBytes[i*4:(i+1)*4], uint32(o))
	}
	if _, err := f.Write(offBytes); err != nil {
		return err
	}
	return nil
}

func loadBinary(path string) (*IVFIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(header[0:4]) != ivfMagic {
		return nil, fmt.Errorf("invalid magic")
	}

	n := int(binary.LittleEndian.Uint32(header[4:8]))
	nc := int(binary.LittleEndian.Uint32(header[8:12]))

	idx := &IVFIndex{
		Vectors:     make([]Vector, n),
		Labels:      make([]uint8, n),
		Centroids:   make([]Vector, nc),
		Offsets:     make([]int, nc+1),
		NumClusters: nc,
	}

	for i := 0; i < n; i++ {
		raw := unsafe.Slice((*byte)(unsafe.Pointer(&idx.Vectors[i][0])), dims)
		if _, err := io.ReadFull(f, raw); err != nil {
			return nil, err
		}
	}
	if _, err := io.ReadFull(f, idx.Labels); err != nil {
		return nil, err
	}
	for i := 0; i < nc; i++ {
		raw := unsafe.Slice((*byte)(unsafe.Pointer(&idx.Centroids[i][0])), dims)
		if _, err := io.ReadFull(f, raw); err != nil {
			return nil, err
		}
	}

	offBytes := make([]byte, (nc+1)*4)
	if _, err := io.ReadFull(f, offBytes); err != nil {
		return nil, err
	}
	for i := 0; i <= nc; i++ {
		idx.Offsets[i] = int(binary.LittleEndian.Uint32(offBytes[i*4 : (i+1)*4]))
	}

	fmt.Printf("Loaded IVF index: %d vectors, %d clusters\n", n, nc)
	return idx, nil
}
