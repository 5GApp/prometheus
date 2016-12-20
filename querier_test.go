package tsdb

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

type mockSeriesIterator struct {
	seek   func(int64) bool
	values func() (int64, float64)
	next   func() bool
	err    func() error
}

func (m *mockSeriesIterator) Seek(t int64) bool        { return m.seek(t) }
func (m *mockSeriesIterator) Values() (int64, float64) { return m.values() }
func (m *mockSeriesIterator) Next() bool               { return m.next() }
func (m *mockSeriesIterator) Err() error               { return m.err() }

type mockSeries struct {
	labels   func() Labels
	iterator func() SeriesIterator
}

func (m *mockSeries) Labels() Labels           { return m.labels() }
func (m *mockSeries) Iterator() SeriesIterator { return m.iterator() }

type listSeriesIterator struct {
	list []sample
	idx  int
}

func newListSeriesIterator(list []sample) *listSeriesIterator {
	return &listSeriesIterator{list: list, idx: -1}
}

func (it *listSeriesIterator) Values() (int64, float64) {
	s := it.list[it.idx]
	return s.t, s.v
}

func (it *listSeriesIterator) Next() bool {
	it.idx++
	return it.idx < len(it.list)
}

func (it *listSeriesIterator) Seek(t int64) bool {
	// Do binary search between current position and end.
	it.idx = sort.Search(len(it.list)-it.idx, func(i int) bool {
		s := it.list[i+it.idx]
		return s.t >= t
	})
	return it.idx < len(it.list)
}

func (it *listSeriesIterator) Err() error {
	return nil
}

// func TestChainedSeriesIterator(t *testing.T) {

// 	cases := []struct {
// 		series []Series
// 	}{}

// 	for _, c := range cases {

// 	}
// }

type mockSeriesSet struct {
	next   func() bool
	series func() Series
	err    func() error
}

func (m *mockSeriesSet) Next() bool     { return m.next() }
func (m *mockSeriesSet) Series() Series { return m.series() }
func (m *mockSeriesSet) Err() error     { return m.err() }

func newListSeriesSet(list []Series) *mockSeriesSet {
	i := -1
	return &mockSeriesSet{
		next: func() bool {
			i++
			return i < len(list)
		},
		series: func() Series {
			return list[i]
		},
		err: func() error { return nil },
	}
}

func TestShardSeriesSet(t *testing.T) {
	newSeries := func(l map[string]string, s []sample) Series {
		return &mockSeries{
			labels:   func() Labels { return LabelsFromMap(l) },
			iterator: func() SeriesIterator { return newListSeriesIterator(s) },
		}
	}

	cases := []struct {
		// The input sets in order (samples in series in b are strictly
		// after those in a).
		a, b SeriesSet
		// The composition of a and b in the shard series set must yield
		// results equivalent to the result series set.
		exp SeriesSet
	}{
		{
			a: newListSeriesSet([]Series{
				newSeries(map[string]string{
					"a": "a",
				}, []sample{
					{t: 1, v: 1},
				}),
			}),
			b: newListSeriesSet([]Series{
				newSeries(map[string]string{
					"a": "a",
				}, []sample{
					{t: 2, v: 2},
				}),
				newSeries(map[string]string{
					"b": "b",
				}, []sample{
					{t: 1, v: 1},
				}),
			}),
			exp: newListSeriesSet([]Series{
				newSeries(map[string]string{
					"a": "a",
				}, []sample{
					{t: 1, v: 1},
					{t: 2, v: 2},
				}),
				newSeries(map[string]string{
					"b": "b",
				}, []sample{
					{t: 1, v: 1},
				}),
			}),
		},
	}

Outer:
	for _, c := range cases {
		res := newShardSeriesSet(c.a, c.b)

		for {
			eok, rok := c.exp.Next(), res.Next()
			require.Equal(t, eok, rok, "next")

			if !eok {
				continue Outer
			}
			sexp := c.exp.Series()
			sres := res.Series()

			require.Equal(t, sexp.Labels(), sres.Labels(), "labels")

			smplExp, errExp := expandSeriesIterator(sexp.Iterator())
			smplRes, errRes := expandSeriesIterator(sres.Iterator())

			require.Equal(t, errExp, errRes, "samples error")
			require.Equal(t, smplExp, smplRes, "samples")
		}
	}
}

func expandSeriesIterator(it SeriesIterator) (r []sample, err error) {
	for it.Next() {
		t, v := it.Values()
		r = append(r, sample{t: t, v: v})
	}

	return r, it.Err()
}

func TestCompareLabels(t *testing.T) {
	cases := []struct {
		a, b []Label
		res  int
	}{
		{
			a:   []Label{},
			b:   []Label{},
			res: 0,
		},
		{
			a:   []Label{{"a", ""}},
			b:   []Label{{"a", ""}, {"b", ""}},
			res: -1,
		},
		{
			a:   []Label{{"a", ""}},
			b:   []Label{{"a", ""}},
			res: 0,
		},
		{
			a:   []Label{{"aa", ""}, {"aa", ""}},
			b:   []Label{{"aa", ""}, {"ab", ""}},
			res: -1,
		},
		{
			a:   []Label{{"aa", ""}, {"abb", ""}},
			b:   []Label{{"aa", ""}, {"ab", ""}},
			res: 1,
		},
		{
			a: []Label{
				{"__name__", "go_gc_duration_seconds"},
				{"job", "prometheus"},
				{"quantile", "0.75"},
			},
			b: []Label{
				{"__name__", "go_gc_duration_seconds"},
				{"job", "prometheus"},
				{"quantile", "1"},
			},
			res: -1,
		},
	}
	for _, c := range cases {
		// Use constructor to ensure sortedness.
		a, b := NewLabels(c.a...), NewLabels(c.b...)

		require.Equal(t, c.res, compareLabels(a, b))
	}
}

func TestSampleRing(t *testing.T) {
	cases := []struct {
		input []int64
		delta int64
		size  int
	}{
		{
			input: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			delta: 2,
			size:  1,
		},
		{
			input: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			delta: 2,
			size:  2,
		},
		{
			input: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			delta: 7,
			size:  3,
		},
		{
			input: []int64{1, 2, 3, 4, 5, 16, 17, 18, 19, 20},
			delta: 7,
			size:  1,
		},
	}
	for _, c := range cases {
		r := newSampleRing(c.delta, c.size)

		input := []sample{}
		for _, t := range c.input {
			input = append(input, sample{
				t: t,
				v: float64(rand.Intn(100)),
			})
		}

		for i, s := range input {
			r.add(s.t, s.v)
			buffered := r.samples()

			for _, sold := range input[:i] {
				found := false
				for _, bs := range buffered {
					if bs.t == sold.t && bs.v == sold.v {
						found = true
						break
					}
				}
				if sold.t >= s.t-c.delta && !found {
					t.Fatalf("%d: expected sample %d to be in buffer but was not; buffer %v", i, sold.t, buffered)
				}
				if sold.t < s.t-c.delta && found {
					t.Fatalf("%d: unexpected sample %d in buffer; buffer %v", i, sold.t, buffered)
				}
			}
		}
	}
}
