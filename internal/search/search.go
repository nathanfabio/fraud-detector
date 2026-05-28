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
const numParts = 16
const windowSize = 50000
const magic = 0x46524546

type ReferenceData struct {
	Parts [numParts]partition
}

type partition struct {
	Vectors []int16
	Frauds  []bool
	Count   int
	Sorted  []int32
}

func (rd *ReferenceData) Total() int {
	t := 0
	for i := 0; i < numParts; i++ {
		t += rd.Parts[i].Count
	}
	return t
}

func Preprocess(inputPath, outputPath string) error {
	in, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer in.Close()

	var reader io.Reader = in
	buf := make([]byte, 2)
	if _, err := io.ReadFull(in, buf); err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if buf[0] == 0x1f && buf[1] == 0x8b {
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek: %w", err)
		}
		gz, err := gzip.NewReader(in)
		if err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		reader = gz
	} else {
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek: %w", err)
		}
	}

	type ref struct {
		Vector []float64 `json:"vector"`
		Label  string    `json:"label"`
	}

	decoder := json.NewDecoder(reader)
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("array start: %w", err)
	}

	type record struct {
		vec   [dims]int16
		fraud bool
	}
	parts := make([][]record, numParts)
	var counts [numParts]int32

	for decoder.More() {
		var r ref
		if err := decoder.Decode(&r); err != nil {
			return fmt.Errorf("decode: %w", err)
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
		counts[k]++
		parts[k] = append(parts[k], record{vec, r.Label == "fraud"})
	}

	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("array end: %w", err)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:4], magic)
	total := int32(0)
	for i := 0; i < numParts; i++ {
		total += counts[i]
	}
	binary.LittleEndian.PutUint32(header[4:8], uint32(total))
	if _, err := out.Write(header); err != nil {
		return err
	}

	for i := 0; i < numParts; i++ {
		binary.LittleEndian.PutUint32(header[0:4], uint32(counts[i]))
		if _, err := out.Write(header[:4]); err != nil {
			return err
		}
	}

	for i := 0; i < numParts; i++ {
		for _, rec := range parts[i] {
			for d := 0; d < dims; d++ {
				binary.LittleEndian.PutUint16(buf[0:2], uint16(rec.vec[d]))
				if _, err := out.Write(buf[:2]); err != nil {
					return err
				}
			}
		}
		for _, rec := range parts[i] {
			b := byte(0)
			if rec.fraud {
				b = 1
			}
			if _, err := out.Write([]byte{b}); err != nil {
				return err
			}
		}
	}

	fmt.Printf("Preprocessed %d vectors to %s\n", total, outputPath)
	return nil
}

func LoadBinary(path string) (*ReferenceData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if binary.LittleEndian.Uint32(header[0:4]) != magic {
		return nil, fmt.Errorf("invalid magic")
	}

	rd := &ReferenceData{}
	var counts [numParts]int32
	for i := 0; i < numParts; i++ {
		if _, err := io.ReadFull(f, header[:4]); err != nil {
			return nil, fmt.Errorf("read count %d: %w", i, err)
		}
		counts[i] = int32(binary.LittleEndian.Uint32(header[:4]))
	}

	for i := 0; i < numParts; i++ {
		n := int(counts[i])
		rd.Parts[i].Count = n
		if n == 0 {
			continue
		}
		rd.Parts[i].Vectors = make([]int16, n*dims)
		rd.Parts[i].Frauds = make([]bool, n)
	}

	for i := 0; i < numParts; i++ {
		n := int(counts[i])
		if n == 0 {
			continue
		}

		vecBytes := make([]byte, n*dims*2)
		if _, err := io.ReadFull(f, vecBytes); err != nil {
			return nil, fmt.Errorf("read vectors %d: %w", i, err)
		}
		for j := 0; j < n*dims; j++ {
			rd.Parts[i].Vectors[j] = int16(binary.LittleEndian.Uint16(vecBytes[j*2 : (j+1)*2]))
		}

		fraudBytes := make([]byte, n)
		if _, err := io.ReadFull(f, fraudBytes); err != nil {
			return nil, fmt.Errorf("read frauds %d: %w", i, err)
		}
		for j := 0; j < n; j++ {
			rd.Parts[i].Frauds[j] = fraudBytes[j] != 0
		}
	}

	fmt.Printf("Loaded %d vectors across %d partitions from binary\n", rd.Total(), numParts)
	return rd, nil
}

func (rd *ReferenceData) BuildIndex() {
	for i := 0; i < numParts; i++ {
		if rd.Parts[i].Count > 0 {
			rd.Parts[i].Sorted = makeSortedIndex(rd.Parts[i].Vectors, rd.Parts[i].Count)
		}
	}
	fmt.Println("Index built")
}

func makeSortedIndex(vectors []int16, count int) []int32 {
	const buckets = 65536
	offsets := make([]int32, buckets)
	for i := 0; i < count; i++ {
		offsets[uint16(vectors[i*dims])]++
	}
	var total int32
	for i := 0; i < buckets; i++ {
		t := offsets[i]
		offsets[i] = total
		total += t
	}
	idx := make([]int32, count)
	for i := 0; i < count; i++ {
		v := uint16(vectors[i*dims])
		pos := offsets[v]
		offsets[v]++
		idx[pos] = int32(i)
	}
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

	k := partKey(query16[:])
	part := &rd.Parts[k]
	if part.Count < topK {
		return 0, topK
	}

	sorted := part.Sorted
	vectors := part.Vectors
	start := binSearch(sorted, vectors, part.Count, query16[0])

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

	q0, q1, q2, q3, q4, q5, q6, q7 := query16[0], query16[1], query16[2], query16[3], query16[4], query16[5], query16[6], query16[7]
	q8, q9, q10, q11, q12, q13 := query16[8], query16[9], query16[10], query16[11], query16[12], query16[13]

	for i := lo; i < hi; i++ {
		idx := int(sorted[i])
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

func binSearch(sorted []int32, vectors []int16, count int, target int16) int {
	lo, hi := 0, count
	for lo < hi {
		mid := (lo + hi) / 2
		if vectors[int(sorted[mid])*dims] < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
