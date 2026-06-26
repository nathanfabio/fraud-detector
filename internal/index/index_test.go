package index

import (
	"os"
	"testing"
)

func TestPartitionTag(t *testing.T) {
	noLastTx := Vector{5: -1}
	if got := PartitionTag(noLastTx); got != 0 {
		t.Errorf("PartitionTag(no lastTx, no flags) = %d, want 0", got)
	}

	hasLastTx := Vector{5: 1000}
	if got := PartitionTag(hasLastTx); got != 1 {
		t.Errorf("PartitionTag(has lastTx) = %d, want 1", got)
	}

	unknownMerchant := Vector{5: -1, 11: 10000}
	if got := PartitionTag(unknownMerchant); got != 2 {
		t.Errorf("PartitionTag(unknown) = %d, want 2", got)
	}

	online := Vector{5: -1, 9: 10000}
	if got := PartitionTag(online); got != 4 {
		t.Errorf("PartitionTag(online) = %d, want 4", got)
	}

	cardPresent := Vector{5: -1, 10: 10000}
	if got := PartitionTag(cardPresent); got != 8 {
		t.Errorf("PartitionTag(cardPresent) = %d, want 8", got)
	}

	all := Vector{5: 1000, 9: 10000, 10: 10000, 11: 10000}
	if got := PartitionTag(all); got != 15 {
		t.Errorf("PartitionTag(all) = %d, want 15", got)
	}
}

func TestQuantize(t *testing.T) {
	if got := quantize(0); got != 0 {
		t.Errorf("quantize(0) = %d, want 0", got)
	}
	if got := quantize(1); got != 10000 {
		t.Errorf("quantize(1) = %d, want 10000", got)
	}
	if got := quantize(-1); got != -1 {
		t.Errorf("quantize(-1) = %d, want -1", got)
	}
	if got := quantize(0.5); got != 5000 {
		t.Errorf("quantize(0.5) = %d, want 5000", got)
	}
	if got := quantize(2.0); got != 10000 {
		t.Errorf("quantize(2.0) = %d, want 10000 (clamped)", got)
	}
	if got := quantize(-0.5); got != 0 {
		t.Errorf("quantize(-0.5) = %d, want 0 (clamped)", got)
	}
}

func TestEuclideanDistSq(t *testing.T) {
	a := Vector{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	b := Vector{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if got := euclideanDistSq(a, b); got != 0 {
		t.Errorf("distance between zeros = %d, want 0", got)
	}

	c := Vector{3, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	d := Vector{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if got := euclideanDistSq(c, d); got != 25 {
		t.Errorf("dist((3,4,...), zero) = %d, want 25", got)
	}
}

func TestEuclideanDistSqEarlyExit(t *testing.T) {
	a := Vector{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	b := Vector{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	got := euclideanDistSqEarlyExit(a, b, 0)
	if got != 0 {
		t.Errorf("distance between zeros = %d, want 0", got)
	}

	c := Vector{1000, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	got = euclideanDistSqEarlyExit(c, a, 0)
	if got != 1000000 {
		t.Errorf("dist with threshold 0 = %d, want 1000000", got)
	}

	gotFull := euclideanDistSqEarlyExit(c, a, 2000000)
	if gotFull != 1000000 {
		t.Errorf("dist with large threshold = %d, want 1000000", gotFull)
	}
}

func TestBBoxDistSq(t *testing.T) {
	q := Vector{5000, 5000, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	inside := Vector{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	bboxHi := Vector{10000, 10000, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if got := bboxDistSq(&q, &inside, &bboxHi); got != 0 {
		t.Errorf("point inside bbox = %d, want 0", got)
	}

	outsideLow := Vector{0, 1000, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	outsideHigh := Vector{1000, 2000, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	got := bboxDistSq(&q, &outsideLow, &outsideHigh)
	if got == 0 {
		t.Errorf("point outside bbox should have nonzero distance")
	}
}

func TestClusterCount(t *testing.T) {
	c := clusterCount(0)
	if c != 0 {
		t.Errorf("clusterCount(0) = %d, want 0 (capped to n)", c)
	}

	c = clusterCount(300 * minClusters)
	if c != minClusters {
		t.Errorf("clusterCount(small) = %d, want %d", c, minClusters)
	}

	largeN := 300 * maxClusters * 10
	c = clusterCount(largeN)
	if c != maxClusters {
		t.Errorf("clusterCount(large) = %d, want %d", c, maxClusters)
	}
}

func TestInsertSortVecDist(t *testing.T) {
	vd := []vecDist{
		{0, 100},
		{1, 10},
		{2, 50},
	}
	insertSortVecDist(vd)
	if vd[0].dist != 10 || vd[1].dist != 50 || vd[2].dist != 100 {
		t.Errorf("insertSortVecDist failed: %v", vd)
	}
}

func TestBuildSubSmall(t *testing.T) {
	records := make([]rawRecord, 100)
	for i := range records {
		records[i] = rawRecord{
			vec: Vector{
				int16(i % 10000),
				int16((i * 7) % 10000),
				int16((i * 13) % 10000),
			},
			label: uint8(i % 2),
		}
	}

	sub := buildSub(records)

	if sub.NumClusters < minClusters || sub.NumClusters > len(records) {
		t.Errorf("NumClusters = %d, expected between %d and %d", sub.NumClusters, minClusters, len(records))
	}
	if len(sub.Vectors) != len(records) {
		t.Errorf("Vectors count = %d, want %d", len(sub.Vectors), len(records))
	}
	if len(sub.Labels) != len(records) {
		t.Errorf("Labels count = %d, want %d", len(sub.Labels), len(records))
	}
	if len(sub.Centroids) != sub.NumClusters {
		t.Errorf("Centroids count = %d, want %d", len(sub.Centroids), sub.NumClusters)
	}
	if len(sub.BBoxMin) != sub.NumClusters {
		t.Errorf("BBoxMin count = %d, want %d", len(sub.BBoxMin), sub.NumClusters)
	}
	if len(sub.BBoxMax) != sub.NumClusters {
		t.Errorf("BBoxMax count = %d, want %d", len(sub.BBoxMax), sub.NumClusters)
	}
	if len(sub.Offsets) != sub.NumClusters+1 {
		t.Errorf("Offsets count = %d, want %d", len(sub.Offsets), sub.NumClusters+1)
	}
	if sub.Offsets[sub.NumClusters] != len(records) {
		t.Errorf("last offset = %d, want %d", sub.Offsets[sub.NumClusters], len(records))
	}
}

func TestBuildSubEmpty(t *testing.T) {
	sub := buildSub(nil)
	if sub.NumClusters != 0 {
		t.Errorf("NumClusters for empty = %d, want 0", sub.NumClusters)
	}
}

func TestIVFIndexSearchEmpty(t *testing.T) {
	idx := &IVFIndex{}
	q := Vector{}
	fraudCount := idx.Search(&q)
	if fraudCount != 0 {
		t.Errorf("search on empty index = %d, want 0", fraudCount)
	}
}

func TestIVFIndexSaveLoadRoundtrip(t *testing.T) {
	records := make([]rawRecord, 300)
	for i := range records {
		label := uint8(0)
		if i < 150 {
			label = 1
		}
		records[i] = rawRecord{
			vec: Vector{
				int16(i % 100),
				int16((i * 7) % 100),
				int16((i * 13) % 100),
				int16((i * 3) % 100),
				int16((i * 11) % 100),
				-1, -1,
				int16(i % 100),
				int16(i % 20),
				0,
				10000,
				0,
				5000,
				int16(i % 100),
			},
			label: label,
		}
	}

	for _, r := range records {
		r.vec[5] = int16(r.vec[0])
		r.vec[6] = int16(r.vec[1])
	}

	idx := &IVFIndex{}
	for tag := 0; tag < numPartitions; tag++ {
		var part []rawRecord
		for _, r := range records {
			if PartitionTag(r.vec) == tag {
				part = append(part, r)
			}
		}
		if len(part) > 0 {
			idx.Parts[tag] = buildSub(part)
		}
	}

	path := "/tmp/test_index.bin"
	defer os.Remove(path)
	if err := idx.saveBinary(path); err != nil {
		t.Fatalf("saveBinary: %v", err)
	}

	loaded, err := LoadBinary(path)
	if err != nil {
		t.Fatalf("loadBinary: %v", err)
	}

	for tag := 0; tag < numPartitions; tag++ {
		orig := idx.Parts[tag]
		load := loaded.Parts[tag]
		if orig == nil && load == nil {
			continue
		}
		if orig == nil || load == nil {
			t.Fatalf("partition %d: one is nil", tag)
		}
		if len(orig.Vectors) != len(load.Vectors) {
			t.Errorf("partition %d: Vectors len mismatch %d vs %d", tag, len(orig.Vectors), len(load.Vectors))
		}
		if orig.NumClusters != load.NumClusters {
			t.Errorf("partition %d: NumClusters mismatch %d vs %d", tag, orig.NumClusters, load.NumClusters)
		}
		for i := range orig.Vectors {
			if orig.Vectors[i] != load.Vectors[i] {
				t.Errorf("partition %d vector %d mismatch", tag, i)
				break
			}
		}
		for i := range orig.Labels {
			if orig.Labels[i] != load.Labels[i] {
				t.Errorf("partition %d label %d mismatch", tag, i)
				break
			}
		}
	}
}

func TestIVFIndexSearch(t *testing.T) {
	records := make([]rawRecord, 400)
	for i := range records {
		label := uint8(0)
		if i%5 == 0 {
			label = 1
		}
		records[i] = rawRecord{
			vec: Vector{
				int16((i * 11) % 10000),
				int16((i * 7) % 10000),
				int16((i * 13) % 10000),
				int16((i * 3) % 10000),
				int16((i * 17) % 10000),
				int16((i * 5) % 10000),
				int16((i * 19) % 10000),
				int16((i * 23) % 10000),
				int16((i * 29) % 10000),
				0,
				10000,
				0,
				int16((i * 31) % 10000),
				int16((i * 37) % 10000),
			},
			label: label,
		}
	}

	idx := &IVFIndex{}
	for tag := 0; tag < numPartitions; tag++ {
		var part []rawRecord
		for _, r := range records {
			if PartitionTag(r.vec) == tag {
				part = append(part, r)
			}
		}
		if len(part) > 0 {
			idx.Parts[tag] = buildSub(part)
		}
	}

	if idx.Parts[PartitionTag(records[0].vec)] == nil {
		t.Fatal("partition for first record is nil")
	}

	fraudCount := idx.Search(&records[0].vec)
	if fraudCount < 0 || fraudCount > 5 {
		t.Errorf("fraudCount = %d, want 0-5", fraudCount)
	}

	q2 := Vector{5000, 5000, 5000, 5000, 5000, 5000, 5000, 5000, 5000, 0, 10000, 0, 5000, 5000}
	_ = idx.Search(&q2)
}
