package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/prompb"

	"github.com/prometheus/prometheus/tsdb/chunkenc"
	sdkcommon "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	monitor "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/monitor/v20180724"
	"github.com/thanos-io/thanos/pkg/component"
	"github.com/thanos-io/thanos/pkg/store/storepb"
	"github.com/u2takey/tencentcloud-thanos-querier/config"
	"google.golang.org/grpc/codes"
)

var statusToCode = map[int]codes.Code{
	http.StatusBadRequest:          codes.InvalidArgument,
	http.StatusNotFound:            codes.NotFound,
	http.StatusUnprocessableEntity: codes.Internal,
	http.StatusServiceUnavailable:  codes.Unavailable,
	http.StatusInternalServerError: codes.Internal,
}

// TencentStore implements the store node API on top of the TencentCloud metrics API.
type TencentStore struct {
	logger    log.Logger
	base      *url.URL
	config    *config.TencentConfig
	buffers   sync.Pool
	component component.StoreAPI
}

const (
	initialBufSize = 32 * 1024 // 32KB seems like a good minimum starting size.
)

// NewPrometheusStore returns a new PrometheusStore that uses the given HTTP client
// to talk to Prometheus.
// It attaches the provided external labels to all results.
func NewTencentCloudStore(
	logger log.Logger,
	config *config.TencentConfig,
	component component.StoreAPI,
) (*TencentStore, error) {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	p := &TencentStore{
		logger:    logger,
		config:    config,
		component: component,
		buffers: sync.Pool{New: func() interface{} {
			b := make([]byte, 0, initialBufSize)
			return &b
		}},
	}
	return p, nil
}

func (p *TencentStore) Info(ctx context.Context, r *storepb.InfoRequest) (*storepb.InfoResponse, error) {
	lset := p.config.ExternalLabels

	res := &storepb.InfoResponse{
		Labels:    make([]storepb.Label, 0, len(lset)),
		StoreType: p.component.ToProto(),
		MinTime:   0,
		MaxTime:   math.MaxInt64,
	}
	for k, v := range lset {
		res.Labels = append(res.Labels, storepb.Label{
			Name:  k,
			Value: v,
		})
	}

	// Until we deprecate the single labels in the reply, we just duplicate
	// them here for migration/compatibility purposes.
	res.LabelSets = []storepb.LabelSet{}
	if len(res.Labels) > 0 {
		res.LabelSets = append(res.LabelSets, storepb.LabelSet{
			Labels: res.Labels,
		})
	}
	return res, nil
}

func (p *TencentStore) getBuffer() *[]byte {
	b := p.buffers.Get()
	return b.(*[]byte)
}

func (p *TencentStore) putBuffer(b *[]byte) {
	p.buffers.Put(b)
}

// Series returns all series for a requested time range and label matcher.
func (p *TencentStore) Series(r *storepb.SeriesRequest, s storepb.Store_SeriesServer) error {
	_ = level.Info(p.logger).Log("SeriesRequest", r)
	return p.queryMonitor(s, r)
}

func (p *TencentStore) chunkSamples(series *monitor.DataPoint) (chks []storepb.AggrChunk, err error) {
	if len(series.Values) == 0 || len(series.Timestamps) == 0 {
		return nil, nil
	}

	c := chunkenc.NewXORChunk()

	a, err := c.Appender()
	if err != nil {
		return nil, err
	}

	for i, s := range series.Values {
		a.Append(int64(*series.Timestamps[i])*1000, *s)
	}

	chks = append(chks, storepb.AggrChunk{
		MinTime: int64(*series.Timestamps[0]) * 1000,
		MaxTime: int64(*series.Timestamps[len(series.Timestamps)-1]) * 1000,
		Raw:     &storepb.Chunk{Type: storepb.Chunk_XOR, Data: c.Bytes()},
	})

	return chks, nil
}

func (p *TencentStore) monitorClient(region string) (client *monitor.Client) {
	client, _ = monitor.NewClient(sdkcommon.NewCredential(
		p.config.Credential.SecretId,
		p.config.Credential.SecretKey,
	), region, profile.NewClientProfile())
	return
}

func makeMonitorInstance(matchers map[string]*storepb.LabelMatcher) (ret []*monitor.Instance) {
	makeDimension := func(n, v string) []*monitor.Dimension {
		return []*monitor.Dimension{{
			Name:  &n,
			Value: &v,
		}}
	}
	for _, m := range matchers {
		if m.Name == "region" || m.Name == "__name__" {
			continue
		}
		ret = append(ret, &monitor.Instance{
			Dimensions: makeDimension(m.Name, m.Value),
		})
	}
	return ret
}

func labelMatcherToMap(matchers []storepb.LabelMatcher) map[string]*storepb.LabelMatcher {
	r := map[string]*storepb.LabelMatcher{}
	for i, a := range matchers {
		// only support LabelMatcher_EQ
		if a.Type == storepb.LabelMatcher_EQ {
			r[a.Name] = &matchers[i]
		}
	}
	return r
}

func safeGetValue(m map[string]*storepb.LabelMatcher, label string) string {
	if a, ok := m[label]; ok {
		return a.Value
	}
	return ""
}

func (p *TencentStore) queryMonitor(s storepb.Store_SeriesServer, q *storepb.SeriesRequest) error {
	labelMap := labelMatcherToMap(q.Matchers)
	region := safeGetValue(labelMap, "region")
	name := safeGetValue(labelMap, "__name__")
	if region == "" {
		return errors.New("region missing")
	}
	if name == "" {
		return errors.New("name missing")
	}
	metricConfig := p.config.Metrics[name]
	if metricConfig == nil {
		return fmt.Errorf("metric %s not support", name)
	}
	//for _, a := range q.Aggregates {
	//	if a > 1 {
	//		return fmt.Errorf("aggregates %s not support", a.String())
	//	}
	//}

	client := p.monitorClient(region)
	request := monitor.NewGetMonitorDataRequest()
	var period uint64 = 60
	request.Namespace = &metricConfig.Namespace
	request.Period = &period
	request.MetricName = &metricConfig.MetricsName
	start := time.Unix(q.MinTime/1000, 0).Format(time.RFC3339)
	end := time.Unix(q.MaxTime/1000, 0).Format(time.RFC3339)
	_ = level.Info(p.logger).Log(start, end)
	request.StartTime = &start
	request.EndTime = &end
	request.Instances = makeMonitorInstance(labelMap)
	res, err := client.GetMonitorData(request)

	b, _ := json.Marshal(request)
	_ = level.Info(p.logger).Log("region", region, "request", string(b))

	b, _ = json.Marshal(res)
	_ = level.Info(p.logger).Log("response", string(b))

	if err != nil {
		return errors.Wrapf(err, "get metrics failed")
	}
	if res.Response == nil {
		return errors.New("getMetric response nil")
	}

	for _, data := range res.Response.DataPoints {
		lset := p.translateAndExtendLabels(data.Dimensions, p.config.ExternalLabels)

		aggregatedChunks, err := p.chunkSamples(data)
		if err != nil {
			return err
		}

		_ = level.Info(p.logger).Log("aggregatedChunks", len(aggregatedChunks))

		if err := s.Send(storepb.NewSeriesResponse(&storepb.Series{
			Labels: lset,
			Chunks: aggregatedChunks,
		})); err != nil {
			return err
		}
	}

	return nil
}

// encodeChunk translates the sample pairs into a chunk.
func (p *TencentStore) encodeChunk(ss []prompb.Sample) (storepb.Chunk_Encoding, []byte, error) {
	c := chunkenc.NewXORChunk()

	a, err := c.Appender()
	if err != nil {
		return 0, nil, err
	}
	for _, s := range ss {
		a.Append(int64(s.Timestamp), float64(s.Value))
	}
	return storepb.Chunk_XOR, c.Bytes(), nil
}

func (p *TencentStore) translateAndExtendLabels(m []*monitor.Dimension, extend map[string]string) []storepb.Label {
	lset := make([]storepb.Label, 0, len(m)+len(extend))

	for _, l := range m {
		if extend[*l.Name] != "" {
			continue
		}
		lset = append(lset, storepb.Label{
			Name:  *l.Name,
			Value: *l.Value,
		})
	}

	return extendLset(lset, extend)
}

func extendLset(lset []storepb.Label, extend map[string]string) []storepb.Label {
	for k, v := range extend {
		lset = append(lset, storepb.Label{
			Name:  k,
			Value: v,
		})
	}
	sort.Slice(lset, func(i, j int) bool {
		return lset[i].Name < lset[j].Name
	})
	return lset
}

// LabelNames returns all known label names.
func (p *TencentStore) LabelNames(
	ctx context.Context, r *storepb.LabelNamesRequest) (*storepb.LabelNamesResponse, error) {
	var labels []string
	_ = level.Info(p.logger).Log("LabelNamesRequest", r)
	for k := range p.config.ExternalLabels {
		labels = append(labels, k)
	}
	labels = append(labels, "region")
	labels = append(labels, "__name__")
	return &storepb.LabelNamesResponse{Names: labels}, nil
}

// LabelValues returns all known label values for a given label name.
func (p *TencentStore) LabelValues(ctx context.Context,
	r *storepb.LabelValuesRequest) (*storepb.LabelValuesResponse, error) {
	_ = level.Info(p.logger).Log("LabelValuesRequest", r)

	externalLset := p.config.ExternalLabels

	// First check for matching external label which has priority.
	if l := externalLset[r.Label]; l != "" {
		return &storepb.LabelValuesResponse{Values: []string{l}}, nil
	}

	if r.Label == "region" {
		return &storepb.LabelValuesResponse{Values: []string{"ap-shanghai", "ap-chengdu", "ap-beijing"}}, nil
	}
	if r.Label == "__name__" {
		ret := []string{}
		for k := range p.config.Metrics {
			ret = append(ret, k)
		}
		return &storepb.LabelValuesResponse{Values: ret}, nil
	}
	return &storepb.LabelValuesResponse{}, nil
}
