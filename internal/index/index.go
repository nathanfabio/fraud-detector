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
const nprobe = 1
const maxScanPerCluster = 2000
const ivfMagic = 0x00415649
const metaClusters = 32
const metaProbe = 4

const numPartitions = 16
const clustersPerPartition = 400

type Vector [dims]int8

type centroidIdx struct {
	metaCentroids []Vector
	members       [][]int32
}

type SubIndex struct {
	Vectors     []Vector
	Labels      []uint8
	Centroids   []Vector
	Offsets     []int
	NumClusters int
	CIdx        centroidIdx
}

type IVFIndex struct {
	Parts [numPartitions]*SubIndex
}

func PartitionTag(vec Vector) int {
	tag := 0
	if vec[10] != 0 {
		tag |= 8
	}
	if vec[9] != 0 {
		tag |= 4
	}
	if vec[11] != 0 {
		tag |= 2
	}
	if vec[5] != -1 {
		tag |= 1
	}
	return tag
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

	partitions := make([][]rawRecord, numPartitions)
	for i := 0; i < numPartitions; i++ {
		partitions[i] = make([]rawRecord, 0)
	}
	for _, r := range records {
		tag := PartitionTag(r.vec)
		partitions[tag] = append(partitions[tag], r)
	}
	records = nil

	for tag := 0; tag < numPartitions; tag++ {
		fmt.Printf("Partition %d: %d records\n", tag, len(partitions[tag]))
	}

	idx := &IVFIndex{}
	for tag := 0; tag < numPartitions; tag++ {
		idx.Parts[tag] = buildSub(partitions[tag])
	}

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

func buildSub(records []rawRecord) *SubIndex {
	n := len(records)
	nc := clustersPerPartition
	if nc > n {
		nc = n
	}
	if nc < 1 {
		return &SubIndex{}
	}

	rng := rand.New(rand.NewSource(42))

	centroids := make([]Vector, nc)
	kpp := nc
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

	for i := kpp; i < nc && i < n; i++ {
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

		acc := make([][dims]float64, nc)
		counts := make([]int, nc)

		for _, idx := range perm {
			v := records[idx].vec
			bestC := 0
			bestDist := int32(1<<31 - 1)
			for c := 0; c < nc; c++ {
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

		for c := 0; c < nc; c++ {
			if counts[c] > 0 {
				cnt := float64(counts[c])
				for d := 0; d < dims; d++ {
					centroids[c][d] = int8(math.Round(acc[c][d] / cnt))
				}
			}
		}
	}

	assignments := make([]int, n)
	clusterCounts := make([]int, nc)
	for i := 0; i < n; i++ {
		bestC := 0
		bestDist := int32(1<<31 - 1)
		for c := 0; c < nc; c++ {
			d := manhattanDist(records[i].vec, centroids[c])
			if d < bestDist {
				bestDist = d
				bestC = c
			}
		}
		assignments[i] = bestC
		clusterCounts[bestC]++
	}

	byCluster := make([][]int, nc)
	for c := 0; c < nc; c++ {
		byCluster[c] = make([]int, 0, clusterCounts[c])
	}
	for i := 0; i < n; i++ {
		byCluster[assignments[i]] = append(byCluster[assignments[i]], i)
	}

	sub := &SubIndex{
		Vectors:     make([]Vector, n),
		Labels:      make([]uint8, n),
		Centroids:   centroids,
		Offsets:     make([]int, nc+1),
		NumClusters: nc,
	}

	pos := 0
	for c := 0; c < nc; c++ {
		sub.Offsets[c] = pos
		for _, origIdx := range byCluster[c] {
			sub.Vectors[pos] = records[origIdx].vec
			sub.Labels[pos] = records[origIdx].label
			pos++
		}
	}
	sub.Offsets[nc] = n

	return sub
}

func (sub *SubIndex) search(query *Vector) int {
	if sub.NumClusters == 0 {
		return 0
	}

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

	if len(sub.CIdx.metaCentroids) > 0 {
		var bestMeta [metaProbe]struct {
			m    int
			dist int32
		}
		for i := 0; i < metaProbe; i++ {
			bestMeta[i].m = -1
			bestMeta[i].dist = 1<<31 - 1
		}
		for m := 0; m < len(sub.CIdx.metaCentroids); m++ {
			d := manhattanDist(*query, sub.CIdx.metaCentroids[m])
			j := metaProbe - 1
			for j > 0 && d < bestMeta[j-1].dist {
				bestMeta[j] = bestMeta[j-1]
				j--
			}
			if d < bestMeta[metaProbe-1].dist {
				bestMeta[j] = struct {
					m    int
					dist int32
				}{m, d}
			}
		}
		for mi := 0; mi < metaProbe; mi++ {
			m := bestMeta[mi].m
			if m < 0 {
				continue
			}
			for _, c := range sub.CIdx.members[m] {
				d := manhattanDist(*query, sub.Centroids[c])
				j := nprobe - 1
				for j > 0 && d < bestClusters[j-1].dist {
					bestClusters[j] = bestClusters[j-1]
					j--
				}
				if d < bestClusters[nprobe-1].dist {
					bestClusters[j] = struct {
						c    int
						dist int32
					}{int(c), d}
				}
			}
		}
	} else {
		for c := 0; c < sub.NumClusters; c++ {
			d := manhattanDist(*query, sub.Centroids[c])
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
		start := sub.Offsets[c]
		end := sub.Offsets[c+1]
		count := end - start
		if count > maxScanPerCluster {
			count = maxScanPerCluster
			end = start + count
		}

		for i := start; i < end; i++ {
			d := manhattanDist(*query, sub.Vectors[i])
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
			bestLabels[j] = sub.Labels[i]
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

func (idx *IVFIndex) Search(query *Vector) int {
	tag := PartitionTag(*query)
	sub := idx.Parts[tag]
	if sub == nil {
		return 0
	}
	return sub.search(query)
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

	b := make([]byte, 10)
	binary.LittleEndian.PutUint32(b[0:4], ivfMagic)
	binary.LittleEndian.PutUint16(b[4:6], numPartitions)
	// bytes 6-10 reserved
	if _, err := f.Write(b); err != nil {
		return err
	}

	offsets := make([]uint32, numPartitions)
	offset := uint32(10 + numPartitions*10)
	for tag := 0; tag < numPartitions; tag++ {
		offsets[tag] = offset
		sub := idx.Parts[tag]
		n := 0
		nc := 0
		if sub != nil {
			n = len(sub.Vectors)
			nc = sub.NumClusters
		}
		offset += uint32(n*14 + n + nc*14 + (nc+1)*4)
	}

	for tag := 0; tag < numPartitions; tag++ {
		sub := idx.Parts[tag]
		n := 0
		nc := 0
		if sub != nil {
			n = len(sub.Vectors)
			nc = sub.NumClusters
		}
		entry := make([]byte, 10)
		binary.LittleEndian.PutUint32(entry[0:4], offsets[tag])
		binary.LittleEndian.PutUint32(entry[4:8], uint32(n))
		binary.LittleEndian.PutUint16(entry[8:10], uint16(nc))
		f.Write(entry)
	}

	for tag := 0; tag < numPartitions; tag++ {
		sub := idx.Parts[tag]
		if sub == nil {
			continue
		}
		for _, v := range sub.Vectors {
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), dims)
			f.Write(raw)
		}
		f.Write(sub.Labels)
		for _, c := range sub.Centroids {
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&c[0])), dims)
			f.Write(raw)
		}
		offBytes := make([]byte, (sub.NumClusters+1)*4)
		for i, o := range sub.Offsets {
			binary.LittleEndian.PutUint32(offBytes[i*4:(i+1)*4], uint32(o))
		}
		f.Write(offBytes)
	}

	return nil
}

func loadBinary(path string) (*IVFIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	header := make([]byte, 10)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(header[0:4]) != ivfMagic {
		return nil, fmt.Errorf("invalid magic")
	}

	nParts := int(binary.LittleEndian.Uint16(header[4:6]))

	offsets := make([]uint32, nParts)
	numVecs := make([]int, nParts)
	numClus := make([]int, nParts)

	for i := 0; i < nParts; i++ {
		entry := make([]byte, 10)
		if _, err := io.ReadFull(f, entry); err != nil {
			return nil, err
		}
		offsets[i] = binary.LittleEndian.Uint32(entry[0:4])
		numVecs[i] = int(binary.LittleEndian.Uint32(entry[4:8]))
		numClus[i] = int(binary.LittleEndian.Uint16(entry[8:10]))
	}

	idx := &IVFIndex{}

	for i := 0; i < nParts; i++ {
		if numVecs[i] == 0 {
			continue
		}
		n := numVecs[i]
		nc := numClus[i]

		sub := &SubIndex{
			Vectors:     make([]Vector, n),
			Labels:      make([]uint8, n),
			Centroids:   make([]Vector, nc),
			Offsets:     make([]int, nc+1),
			NumClusters: nc,
		}

		for vi := 0; vi < n; vi++ {
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&sub.Vectors[vi][0])), dims)
			if _, err := io.ReadFull(f, raw); err != nil {
				return nil, err
			}
		}
		if _, err := io.ReadFull(f, sub.Labels); err != nil {
			return nil, err
		}
		for ci := 0; ci < nc; ci++ {
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&sub.Centroids[ci][0])), dims)
			if _, err := io.ReadFull(f, raw); err != nil {
				return nil, err
			}
		}
		offBytes := make([]byte, (nc+1)*4)
		if _, err := io.ReadFull(f, offBytes); err != nil {
			return nil, err
		}
		for oi := 0; oi <= nc; oi++ {
			sub.Offsets[oi] = int(binary.LittleEndian.Uint32(offBytes[oi*4 : (oi+1)*4]))
		}

		sub.buildCentroidIndex()
		idx.Parts[i] = sub
	}

	total := 0
	for i := 0; i < nParts; i++ {
		total += numVecs[i]
	}
	fmt.Printf("Loaded partitioned IVF index: %d vectors across %d partitions\n", total, nParts)
	return idx, nil
}

func (sub *SubIndex) buildCentroidIndex() {
	nc := sub.NumClusters
	mc := metaClusters
	if nc < mc {
		mc = nc
	}
	if mc < 2 {
		return
	}

	rng := rand.New(rand.NewSource(12345))

	meta := make([]Vector, mc)
	for i := 0; i < mc && i < nc; i++ {
		meta[i] = sub.Centroids[i]
	}
	assignments := make([]int, nc)

	for iter := 0; iter < 10; iter++ {
		for i := 0; i < nc; i++ {
			best := 0
			bestDist := int32(1<<31 - 1)
			for m := 0; m < mc; m++ {
				d := manhattanDist(sub.Centroids[i], meta[m])
				if d < bestDist {
					bestDist = d
					best = m
				}
			}
			assignments[i] = best
		}
		counts := make([]int, mc)
		acc := make([][]float64, mc)
		for m := 0; m < mc; m++ {
			acc[m] = make([]float64, dims)
		}
		for i := 0; i < nc; i++ {
			m := assignments[i]
			counts[m]++
			for d := 0; d < dims; d++ {
				acc[m][d] += float64(sub.Centroids[i][d])
			}
		}
		for m := 0; m < mc; m++ {
			if counts[m] > 0 {
				cnt := float64(counts[m])
				for d := 0; d < dims; d++ {
					meta[m][d] = int8(math.Round(acc[m][d] / cnt))
				}
			}
		}
		_ = rng
	}

	members := make([][]int32, mc)
	for m := 0; m < mc; m++ {
		members[m] = make([]int32, 0)
	}
	for i := 0; i < nc; i++ {
		m := assignments[i]
		members[m] = append(members[m], int32(i))
	}
	sub.CIdx = centroidIdx{
		metaCentroids: meta,
		members:       members,
	}
}
