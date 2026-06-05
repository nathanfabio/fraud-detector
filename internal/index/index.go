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
const nprobe = 24
const ivfMagic = 0x05415649

const numPartitions = 16
const maxClusters = 2048
const minClusters = 64
const clusterDivisor = 300

type Vector [dims]int8

type SubIndex struct {
	Vectors     []Vector
	Labels      []uint8
	Centroids   []Vector
	BBoxMin     []Vector
	BBoxMax     []Vector
	Offsets     []int
	NumClusters int
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

func clusterCount(n int) int {
	k := n / clusterDivisor
	if k < minClusters {
		k = minClusters
	}
	if k > maxClusters {
		k = maxClusters
	}
	if k > n {
		k = n
	}
	return k
}

type vecDist struct {
	idx  int
	dist int32
}

func insertSortVecDist(vd []vecDist) {
	for i := 1; i < len(vd); i++ {
		j := i
		for j > 0 && vd[j].dist < vd[j-1].dist {
			vd[j], vd[j-1] = vd[j-1], vd[j]
			j--
		}
	}
}

func buildSub(records []rawRecord) *SubIndex {
	n := len(records)
	nc := clusterCount(n)
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
					d := int64(euclideanDistSq(records[j].vec, centroids[k]))
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
			bestDist := int32(math.MaxInt32)
			for c := 0; c < nc; c++ {
				d := euclideanDistSq(v, centroids[c])
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
		bestDist := int32(math.MaxInt32)
		for c := 0; c < nc; c++ {
			d := euclideanDistSq(records[i].vec, centroids[c])
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

	bboxMin := make([]Vector, nc)
	bboxMax := make([]Vector, nc)
	for c := 0; c < nc; c++ {
		if len(byCluster[c]) == 0 {
			continue
		}
		first := records[byCluster[c][0]].vec
		bboxMin[c] = first
		bboxMax[c] = first
		for _, origIdx := range byCluster[c] {
			v := records[origIdx].vec
			for d := 0; d < dims; d++ {
				if v[d] < bboxMin[c][d] {
					bboxMin[c][d] = v[d]
				}
				if v[d] > bboxMax[c][d] {
					bboxMax[c][d] = v[d]
				}
			}
		}
	}

	for c := 0; c < nc; c++ {
		cluster := byCluster[c]
		if len(cluster) <= 1 {
			continue
		}
		vd := make([]vecDist, len(cluster))
		for i, origIdx := range cluster {
			vd[i] = vecDist{origIdx, euclideanDistSq(records[origIdx].vec, centroids[c])}
		}
		insertSortVecDist(vd)
		for i := range cluster {
			cluster[i] = vd[i].idx
		}
	}

	sub := &SubIndex{
		Vectors:     make([]Vector, n),
		Labels:      make([]uint8, n),
		Centroids:   centroids,
		BBoxMin:     bboxMin,
		BBoxMax:     bboxMax,
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

func bboxDistSq(query *Vector, bmin, bmax *Vector) int32 {
	var sum int32
	for d := 0; d < dims; d++ {
		q := int32(query[d])
		lo := int32(bmin[d])
		hi := int32(bmax[d])
		if q < lo {
			diff := lo - q
			sum += diff * diff
		} else if q > hi {
			diff := q - hi
			sum += diff * diff
		}
	}
	return sum
}

type probeEntry struct {
	c    int
	dist int32
}

func insertProbe(probes *[]probeEntry, c int, dist int32) {
	for i := len(*probes) - 1; i >= 0; i-- {
		if dist >= (*probes)[i].dist {
			idx := i + 1
			if idx < nprobe {
				if len(*probes) < nprobe {
					*probes = append(*probes, probeEntry{})
				}
				copy((*probes)[idx+1:], (*probes)[idx:len(*probes)-1])
				(*probes)[idx] = probeEntry{c, dist}
			}
			return
		}
	}
	if len(*probes) < nprobe {
		*probes = append(*probes, probeEntry{})
	}
	copy((*probes)[1:], (*probes)[:len(*probes)-1])
	(*probes)[0] = probeEntry{c, dist}
}

func (sub *SubIndex) search(query *Vector) int {
	if sub.NumClusters == 0 {
		return 0
	}

	probes := make([]probeEntry, 0, nprobe)

	for c := 0; c < sub.NumClusters; c++ {
		d := bboxDistSq(query, &sub.BBoxMin[c], &sub.BBoxMax[c])
		insertProbe(&probes, c, d)
	}

	var bestDist [topK]int32
	var bestLabels [topK]uint8
	for i := 0; i < topK; i++ {
		bestDist[i] = math.MaxInt32
	}

	for ci := 0; ci < len(probes); ci++ {
		c := probes[ci].c
		start := sub.Offsets[c]
		end := sub.Offsets[c+1]
		threshold := bestDist[topK-1]

		for i := start; i < end; i++ {
			d := euclideanDistSqEarlyExit(*query, sub.Vectors[i], threshold)
			if d >= threshold {
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
			threshold = bestDist[topK-1]
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

func euclideanDistSq(a, b Vector) int32 {
	d0 := int32(a[0]) - int32(b[0])
	d1 := int32(a[1]) - int32(b[1])
	d2 := int32(a[2]) - int32(b[2])
	d3 := int32(a[3]) - int32(b[3])
	d4 := int32(a[4]) - int32(b[4])
	d5 := int32(a[5]) - int32(b[5])
	d6 := int32(a[6]) - int32(b[6])
	d7 := int32(a[7]) - int32(b[7])
	d8 := int32(a[8]) - int32(b[8])
	d9 := int32(a[9]) - int32(b[9])
	da := int32(a[10]) - int32(b[10])
	db := int32(a[11]) - int32(b[11])
	dc := int32(a[12]) - int32(b[12])
	dd := int32(a[13]) - int32(b[13])
	return d0*d0 + d1*d1 + d2*d2 + d3*d3 + d4*d4 + d5*d5 + d6*d6 + d7*d7 + d8*d8 + d9*d9 + da*da + db*db + dc*dc + dd*dd
}

func euclideanDistSqEarlyExit(a, b Vector, threshold int32) int32 {
	d0 := int32(a[0]) - int32(b[0])
	sum := d0 * d0
	if sum > threshold {
		return sum
	}
	d1 := int32(a[1]) - int32(b[1])
	sum += d1 * d1
	if sum > threshold {
		return sum
	}
	d2 := int32(a[2]) - int32(b[2])
	sum += d2 * d2
	if sum > threshold {
		return sum
	}
	d3 := int32(a[3]) - int32(b[3])
	sum += d3 * d3
	if sum > threshold {
		return sum
	}
	d4 := int32(a[4]) - int32(b[4])
	sum += d4 * d4
	if sum > threshold {
		return sum
	}
	d5 := int32(a[5]) - int32(b[5])
	sum += d5 * d5
	if sum > threshold {
		return sum
	}
	d6 := int32(a[6]) - int32(b[6])
	sum += d6 * d6
	if sum > threshold {
		return sum
	}
	d7 := int32(a[7]) - int32(b[7])
	sum += d7 * d7
	if sum > threshold {
		return sum
	}
	d8 := int32(a[8]) - int32(b[8])
	sum += d8 * d8
	if sum > threshold {
		return sum
	}
	d9 := int32(a[9]) - int32(b[9])
	sum += d9 * d9
	if sum > threshold {
		return sum
	}
	da := int32(a[10]) - int32(b[10])
	sum += da * da
	if sum > threshold {
		return sum
	}
	db := int32(a[11]) - int32(b[11])
	sum += db * db
	if sum > threshold {
		return sum
	}
	dc := int32(a[12]) - int32(b[12])
	sum += dc * dc
	if sum > threshold {
		return sum
	}
	dd := int32(a[13]) - int32(b[13])
	sum += dd * dd
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
		offset += uint32(n*14 + n + nc*14 + nc*14 + nc*14 + (nc+1)*4)
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
		for _, bm := range sub.BBoxMin {
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&bm[0])), dims)
			f.Write(raw)
		}
		for _, bm := range sub.BBoxMax {
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&bm[0])), dims)
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

	totalOff := make([]uint32, nParts)
	numVecs := make([]int, nParts)
	numClus := make([]int, nParts)

	for i := 0; i < nParts; i++ {
		entry := make([]byte, 10)
		if _, err := io.ReadFull(f, entry); err != nil {
			return nil, err
		}
		totalOff[i] = binary.LittleEndian.Uint32(entry[0:4])
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
			BBoxMin:     make([]Vector, nc),
			BBoxMax:     make([]Vector, nc),
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
		for ci := 0; ci < nc; ci++ {
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&sub.BBoxMin[ci][0])), dims)
			if _, err := io.ReadFull(f, raw); err != nil {
				return nil, err
			}
		}
		for ci := 0; ci < nc; ci++ {
			raw := unsafe.Slice((*byte)(unsafe.Pointer(&sub.BBoxMax[ci][0])), dims)
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

		idx.Parts[i] = sub
	}

	total := 0
	for i := 0; i < nParts; i++ {
		total += numVecs[i]
	}
	fmt.Printf("Loaded partitioned IVF index: %d vectors across %d partitions\n", total, nParts)
	return idx, nil
}
