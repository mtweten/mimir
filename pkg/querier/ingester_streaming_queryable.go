package querier

import (
	"context"
	"sort"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"

	"github.com/weaveworks/common/user"
	"github.com/weaveworks/cortex/pkg/chunk"
	"github.com/weaveworks/cortex/pkg/ingester/client"
	"github.com/weaveworks/cortex/pkg/util/chunkcompat"
)

func newIngesterStreamingQueryable(distributor Distributor, chunkIteratorFunc chunkIteratorFunc) *ingesterQueryable {
	return &ingesterQueryable{
		distributor:       distributor,
		chunkIteratorFunc: chunkIteratorFunc,
	}
}

type ingesterQueryable struct {
	distributor       Distributor
	chunkIteratorFunc chunkIteratorFunc
}

// Querier implements storage.Queryable.
func (i ingesterQueryable) Querier(ctx context.Context, mint, maxt int64) (storage.Querier, error) {
	return &ingesterStreamingQuerier{
		chunkIteratorFunc: i.chunkIteratorFunc,
		distributorQuerier: distributorQuerier{
			distributor: i.distributor,
			ctx:         ctx,
			mint:        mint,
			maxt:        maxt,
		},
	}, nil
}

// Get implements ChunkStore.
func (i ingesterQueryable) Get(ctx context.Context, from, through model.Time, matchers ...*labels.Matcher) ([]chunk.Chunk, error) {
	userID, err := user.ExtractOrgID(ctx)
	if err != nil {
		return nil, promql.ErrStorage(err)
	}

	results, err := i.distributor.QueryStream(ctx, from, through, matchers...)
	if err != nil {
		return nil, promql.ErrStorage(err)
	}

	chunks := make([]chunk.Chunk, 0, len(results))
	for _, result := range results {
		metric := client.FromLabelPairs(result.Labels)
		cs, err := chunkcompat.FromChunks(userID, metric, result.Chunks)
		if err != nil {
			return nil, promql.ErrStorage(err)
		}
		chunks = append(chunks, cs...)
	}

	return chunks, nil
}

type ingesterStreamingQuerier struct {
	chunkIteratorFunc chunkIteratorFunc
	distributorQuerier
}

func (q *ingesterStreamingQuerier) Select(_ *storage.SelectParams, matchers ...*labels.Matcher) (storage.SeriesSet, error) {
	userID, err := user.ExtractOrgID(q.ctx)
	if err != nil {
		return nil, promql.ErrStorage(err)
	}

	results, err := q.distributor.QueryStream(q.ctx, model.Time(q.mint), model.Time(q.maxt), matchers...)
	if err != nil {
		return nil, promql.ErrStorage(err)
	}

	serieses := make([]storage.Series, 0, len(results))
	for _, result := range results {
		metric := client.FromLabelPairs(result.Labels)
		chunks, err := chunkcompat.FromChunks(userID, metric, result.Chunks)
		if err != nil {
			return nil, promql.ErrStorage(err)
		}

		ls := client.FromLabelPairsToLabels(result.Labels)
		sort.Sort(ls)
		series := &chunkSeries{
			labels:            ls,
			chunks:            chunks,
			chunkIteratorFunc: q.chunkIteratorFunc,
		}
		serieses = append(serieses, series)
	}

	return newConcreteSeriesSet(serieses), nil
}
