package tokenusage

import (
	"cmp"
	"database/sql"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
	"time"

	"video-rename/internal/storage"
)

// AnalyticsQuery scopes a usage report. From and To are epoch milliseconds in
// a half-open range [From,To); a zero From means all recorded history. Filters
// apply to every dimension of the report so totals, trend and rankings always
// describe the same slice.
type AnalyticsQuery struct {
	From, To int64
	Location *time.Location
	Model    string // composite provider|protocol|model key, empty for all
	Profile  int64
	Source   string // "", manual or scheduled
	Run      int64
	Sort     string // tokens (default) or recent
	Page     int
	Size     int
}

type Report struct {
	Range      ReportRange    `json:"range"`
	Totals     ReportTotals   `json:"totals"`
	Trend      []TrendBucket  `json:"trend"`
	Models     []ModelGroup   `json:"models"`
	Profiles   []ProfileGroup `json:"profiles"`
	Sources    []SourceGroup  `json:"sources"`
	TopBatches []Batch        `json:"top_batches"`
	Batches    PagedBatches   `json:"batches"`
	Options    ReportOptions  `json:"options"`
}
type ReportRange struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
	Interval string `json:"interval"`
}
type ReportTotals struct {
	Tokens           int64 `json:"tokens"`
	Records          int64 `json:"records"`
	EstimatedRecords int64 `json:"estimated_records"`
	LegacyRecords    int64 `json:"legacy_records"`
	Batches          int64 `json:"batches"`
}
type TrendBucket struct {
	From             string `json:"from"`
	To               string `json:"to"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
	LegacyRecords    int64  `json:"legacy_records"`
}
type ModelGroup struct {
	Key              string `json:"key"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Protocol         string `json:"protocol"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
	LegacyRecords    int64  `json:"legacy_records"`
	Batches          int64  `json:"batches"`
}
type ProfileGroup struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
	LegacyRecords    int64  `json:"legacy_records"`
	Batches          int64  `json:"batches"`
}
type SourceGroup struct {
	Key              string `json:"key"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
	LegacyRecords    int64  `json:"legacy_records"`
	Batches          int64  `json:"batches"`
}
type Batch struct {
	ID               int64  `json:"id"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Protocol         string `json:"protocol"`
	ProfileID        int64  `json:"profile_id"`
	ProfileTitle     string `json:"profile_title"`
	Source           string `json:"source"`
	Status           string `json:"status"`
	Created          string `json:"created"`
	Started          string `json:"started"`
	Finished         string `json:"finished"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
	LegacyRecords    int64  `json:"legacy_records"`
	LifetimeTokens   int64  `json:"lifetime_tokens"`
	LifetimeRecords  int64  `json:"lifetime_records"`
	LastRecordedAt   string `json:"last_recorded_at"`
}
type PagedBatches struct {
	Items []Batch `json:"items"`
	Total int     `json:"total"`
	Page  int     `json:"page"`
	Size  int     `json:"size"`
}
type ReportOptions struct {
	Models   []OptionModel   `json:"models"`
	Profiles []OptionProfile `json:"profiles"`
}
type OptionModel struct {
	Key      string `json:"key"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Protocol string `json:"protocol"`
}
type OptionProfile struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// runMeta is the immutable per-run identity derived from the run snapshot.
type runMeta struct {
	model, provider, protocol, key string
	profileID                      int64
	profileTitle                   string
	source                         string // manual or scheduled
}

// batchAcc accumulates one run's in-range usage while scanning the ledger.
type batchAcc struct {
	id                                       int64
	meta                                     runMeta
	status, created, started, finished       string
	tokens, records, estimated, legacy, last int64
}

func modelKey(provider, protocol, model string) string {
	return provider + "|" + protocol + "|" + model
}

func parseMeta(source string, plan int64, summary string, profileID int64, profileTitle string) runMeta {
	var parsed struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Protocol string `json:"protocol"`
	}
	if summary != "" && json.Unmarshal([]byte(summary), &parsed) != nil {
		parsed.Model, parsed.Provider, parsed.Protocol = "", "", ""
	}
	kind := "manual"
	if source == "schedule" || plan > 0 {
		kind = "scheduled"
	}
	return runMeta{model: parsed.Model, provider: parsed.Provider, protocol: parsed.Protocol, key: modelKey(parsed.Provider, parsed.Protocol, parsed.Model), profileID: profileID, profileTitle: profileTitle, source: kind}
}

// Analytics aggregates the usage ledger inside one read transaction so every
// dimension of the report sees the same snapshot. The ledger holds one row per
// counted model exchange, so grouped aggregation happens in Go instead of six
// SQL passes that could drift apart.
func Analytics(db *storage.Store, q AnalyticsQuery) (*Report, error) {
	if q.Location == nil {
		q.Location = time.UTC
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		if q.Size < 1 {
			q.Size = 20
		} else {
			q.Size = 100
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT r.run,r.recorded_at,r.tokens,r.records,r.estimated_records,r.legacy,u.source,u.plan,u.config_summary,u.model_config_id,u.model_config_title,u.status,u.created,u.started,u.finished
FROM token_usage_records r JOIN runs u ON u.id=r.run
WHERE r.recorded_at>=? AND r.recorded_at<? ORDER BY r.recorded_at,r.id`, q.From, q.To)
	if err != nil {
		return nil, err
	}
	buckets := bucketingFor(q.To-q.From, q.Location)
	trend := map[int64]*TrendBucket{}
	models := map[string]*ModelGroup{}
	profiles := map[int64]*ProfileGroup{}
	sources := map[string]*SourceGroup{}
	batches := map[int64]*batchAcc{}
	type sample struct {
		at, tokens, records, estimated, legacy int64
	}
	var samples []sample
	var totals ReportTotals
	for rows.Next() {
		var run, recordedAt, tokens, records, estimated, legacy, plan, profileID int64
		var source, summary, profileTitle, status, created, started, finished string
		if err = rows.Scan(&run, &recordedAt, &tokens, &records, &estimated, &legacy, &source, &plan, &summary, &profileID, &profileTitle, &status, &created, &started, &finished); err != nil {
			rows.Close()
			return nil, err
		}
		meta := parseMeta(source, plan, summary, profileID, profileTitle)
		if (q.Model != "" && meta.key != q.Model) || (q.Profile > 0 && meta.profileID != q.Profile) || (q.Source != "" && meta.source != q.Source) || (q.Run > 0 && run != q.Run) {
			continue
		}
		counted := records
		totals.Tokens += tokens
		totals.Records += counted
		totals.EstimatedRecords += estimated
		if legacy > 0 {
			totals.LegacyRecords += counted
		}
		samples = append(samples, sample{at: recordedAt, tokens: tokens, records: counted, estimated: estimated, legacy: legacy})
		model := models[meta.key]
		if model == nil {
			model = &ModelGroup{Key: meta.key, Model: meta.model, Provider: meta.provider, Protocol: meta.protocol}
			models[meta.key] = model
		}
		profile := profiles[meta.profileID]
		if profile == nil {
			profile = &ProfileGroup{ID: meta.profileID, Title: meta.profileTitle}
			profiles[meta.profileID] = profile
		}
		origin := sources[meta.source]
		if origin == nil {
			origin = &SourceGroup{Key: meta.source}
			sources[meta.source] = origin
		}
		batch := batches[run]
		if batch == nil {
			batch = &batchAcc{id: run, meta: meta, status: status, created: created, started: started, finished: finished}
			batches[run] = batch
			totals.Batches++
			model.Batches++
			profile.Batches++
			origin.Batches++
		}
		model.Tokens += tokens
		model.Records += counted
		model.EstimatedRecords += estimated
		profile.Tokens += tokens
		profile.Records += counted
		profile.EstimatedRecords += estimated
		origin.Tokens += tokens
		origin.Records += counted
		origin.EstimatedRecords += estimated
		if legacy > 0 {
			model.LegacyRecords += counted
			profile.LegacyRecords += counted
			origin.LegacyRecords += counted
		}
		batch.tokens += tokens
		batch.records += counted
		batch.estimated += estimated
		if legacy > 0 {
			batch.legacy += counted
		}
		if recordedAt > batch.last {
			batch.last = recordedAt
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// "All history" starts at the first recorded usage, not the epoch, so the
	// trend granularity follows the data's real extent instead of decades of
	// empty buckets.
	from := q.From
	if from == 0 && len(samples) > 0 {
		first := samples[0].at
		for _, s := range samples {
			if s.at < first {
				first = s.at
			}
		}
		buckets = bucketingFor(q.To-first, q.Location)
		from = buckets.start(first)
	}
	for _, s := range samples {
		key := buckets.start(s.at)
		bucket := trend[key]
		if bucket == nil {
			bucket = &TrendBucket{}
			trend[key] = bucket
		}
		bucket.Tokens += s.tokens
		bucket.Records += s.records
		bucket.EstimatedRecords += s.estimated
		if s.legacy > 0 {
			bucket.LegacyRecords += s.records
		}
	}
	options, err := reportOptions(tx)
	if err != nil {
		return nil, err
	}
	report := &Report{
		Range: ReportRange{
			From:     time.UnixMilli(from).In(q.Location).Format(time.RFC3339),
			To:       time.UnixMilli(q.To).In(q.Location).Format(time.RFC3339),
			Timezone: q.Location.String(),
			Interval: buckets.name,
		},
		Totals:     totals,
		Trend:      buckets.series(from, q.To, trend),
		Models:     sortedModels(models),
		Profiles:   sortedProfiles(profiles),
		Sources:    sortedSources(sources),
		TopBatches: []Batch{},
		Batches:    PagedBatches{Items: []Batch{}, Page: q.Page, Size: q.Size},
		Options:    options,
	}
	ordered := sortedBatches(batches, q.Sort)
	report.Batches.Total = len(ordered)
	displayed := map[int64]bool{}
	for i, acc := range ordered {
		if i == 10 {
			break
		}
		report.TopBatches = append(report.TopBatches, newBatch(acc))
		displayed[acc.id] = true
	}
	start := (q.Page - 1) * q.Size
	for i := start; i < len(ordered) && i < start+q.Size; i++ {
		if !displayed[ordered[i].id] {
			displayed[ordered[i].id] = true
		}
		report.Batches.Items = append(report.Batches.Items, newBatch(ordered[i]))
	}
	lifetimes, err := lifetimeUsage(tx, displayed)
	if err != nil {
		return nil, err
	}
	for i := range report.TopBatches {
		report.TopBatches[i].LifetimeTokens = lifetimes[report.TopBatches[i].ID][0]
		report.TopBatches[i].LifetimeRecords = lifetimes[report.TopBatches[i].ID][1]
	}
	for i := range report.Batches.Items {
		report.Batches.Items[i].LifetimeTokens = lifetimes[report.Batches.Items[i].ID][0]
		report.Batches.Items[i].LifetimeRecords = lifetimes[report.Batches.Items[i].ID][1]
	}
	return report, tx.Commit()
}

func newBatch(acc *batchAcc) Batch {
	return Batch{ID: acc.id, Model: acc.meta.model, Provider: acc.meta.provider, Protocol: acc.meta.protocol, ProfileID: acc.meta.profileID, ProfileTitle: acc.meta.profileTitle, Source: acc.meta.source, Status: acc.status, Created: acc.created, Started: acc.started, Finished: acc.finished, Tokens: acc.tokens, Records: acc.records, EstimatedRecords: acc.estimated, LegacyRecords: acc.legacy, LastRecordedAt: time.UnixMilli(acc.last).UTC().Format(time.RFC3339)}
}

// lifetimeUsage reads the run aggregates for the displayed batches. They cover
// all recorded history, so a batch filtered to part of a range still shows its
// full lifetime next to the in-range numbers.
func lifetimeUsage(tx *sql.Tx, ids map[int64]bool) (map[int64][2]int64, error) {
	out := map[int64][2]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	keys := slices.Sorted(maps.Keys(ids))
	placeholders := strings.TrimRight(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, len(keys))
	for i, id := range keys {
		args[i] = id
	}
	rows, err := tx.Query("SELECT run,token_count,token_records FROM run_token_usage WHERE run IN ("+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var run, tokens, records int64
		if err = rows.Scan(&run, &tokens, &records); err != nil {
			return nil, err
		}
		out[run] = [2]int64{tokens, records}
	}
	return out, rows.Err()
}

// reportOptions lists the models and profiles that ever recorded usage, so the
// page can offer filters even when the current range shows no rows for them.
func reportOptions(tx *sql.Tx) (ReportOptions, error) {
	rows, err := tx.Query(`SELECT DISTINCT u.source,u.plan,u.config_summary,u.model_config_id,u.model_config_title FROM runs u JOIN token_usage_records r ON r.run=u.id`)
	if err != nil {
		return ReportOptions{}, err
	}
	defer rows.Close()
	models := map[string]OptionModel{}
	profiles := map[int64]string{}
	for rows.Next() {
		var source, summary, title string
		var plan, profileID int64
		if err = rows.Scan(&source, &plan, &summary, &profileID, &title); err != nil {
			return ReportOptions{}, err
		}
		meta := parseMeta(source, plan, summary, profileID, title)
		if _, ok := models[meta.key]; !ok {
			models[meta.key] = OptionModel{Key: meta.key, Model: meta.model, Provider: meta.provider, Protocol: meta.protocol}
		}
		if _, ok := profiles[profileID]; !ok {
			profiles[profileID] = title
		}
	}
	if err = rows.Err(); err != nil {
		return ReportOptions{}, err
	}
	out := ReportOptions{Models: []OptionModel{}, Profiles: []OptionProfile{}}
	for _, model := range models {
		out.Models = append(out.Models, model)
	}
	slices.SortFunc(out.Models, func(a, b OptionModel) int {
		if c := cmp.Compare(a.Model, b.Model); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	for id, title := range profiles {
		out.Profiles = append(out.Profiles, OptionProfile{ID: id, Title: title})
	}
	slices.SortFunc(out.Profiles, func(a, b OptionProfile) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func sortedModels(models map[string]*ModelGroup) []ModelGroup {
	out := make([]ModelGroup, 0, len(models))
	for _, model := range models {
		out = append(out, *model)
	}
	slices.SortFunc(out, func(a, b ModelGroup) int {
		if c := cmp.Compare(b.Tokens, a.Tokens); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return out
}

func sortedProfiles(profiles map[int64]*ProfileGroup) []ProfileGroup {
	out := make([]ProfileGroup, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, *profile)
	}
	slices.SortFunc(out, func(a, b ProfileGroup) int {
		if c := cmp.Compare(b.Tokens, a.Tokens); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

func sortedSources(sources map[string]*SourceGroup) []SourceGroup {
	out := make([]SourceGroup, 0, len(sources))
	for _, source := range sources {
		out = append(out, *source)
	}
	slices.SortFunc(out, func(a, b SourceGroup) int {
		if c := cmp.Compare(b.Tokens, a.Tokens); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return out
}

func sortedBatches(batches map[int64]*batchAcc, sortKey string) []*batchAcc {
	out := make([]*batchAcc, 0, len(batches))
	for _, batch := range batches {
		out = append(out, batch)
	}
	slices.SortFunc(out, func(a, b *batchAcc) int {
		if sortKey == "recent" {
			if c := cmp.Compare(b.last, a.last); c != 0 {
				return c
			}
			return cmp.Compare(b.id, a.id)
		}
		if c := cmp.Compare(b.tokens, a.tokens); c != 0 {
			return c
		}
		return cmp.Compare(b.id, a.id)
	})
	return out
}

// bucketing picks a trend granularity for the requested span. Calendar
// buckets align to local midnths so day and month boundaries follow the
// reader's timezone, including DST transitions.
type bucketing struct {
	name string
	step int64 // fixed-step milliseconds; zero for calendar buckets
	loc  *time.Location
}

func bucketingFor(span int64, loc *time.Location) bucketing {
	const hour, day = int64(time.Hour / time.Millisecond), int64(24 * time.Hour / time.Millisecond)
	switch {
	case span <= 6*hour:
		return bucketing{name: "15m", step: 15 * 60 * 1000, loc: loc}
	case span <= 72*hour:
		return bucketing{name: "hour", step: hour, loc: loc}
	case span <= 120*day:
		return bucketing{name: "day", loc: loc}
	case span <= 10*365*day:
		return bucketing{name: "month", loc: loc}
	default:
		return bucketing{name: "year", loc: loc}
	}
}

func align(ms, size int64) int64 {
	return ms - ms%size
}

func (b bucketing) start(ms int64) int64 {
	t := time.UnixMilli(ms).In(b.loc)
	switch b.name {
	case "15m":
		return align(ms, b.step)
	case "hour":
		return align(ms, b.step)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, b.loc).UnixMilli()
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, b.loc).UnixMilli()
	default:
		return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, b.loc).UnixMilli()
	}
}

func (b bucketing) next(ms int64) int64 {
	t := time.UnixMilli(ms).In(b.loc)
	switch b.name {
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, b.loc).AddDate(0, 0, 1).UnixMilli()
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, b.loc).AddDate(0, 1, 0).UnixMilli()
	case "year":
		return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, b.loc).AddDate(1, 0, 0).UnixMilli()
	default:
		return ms + b.step
	}
}

// series renders the trend across the effective range, keeping zero buckets
// for quiet stretches instead of collapsing them.
func (b bucketing) series(from, to int64, trend map[int64]*TrendBucket) []TrendBucket {
	out := []TrendBucket{}
	end := b.start(to - 1)
	for cursor, guard := b.start(from), 0; cursor <= end && guard < 2000; cursor, guard = b.next(cursor), guard+1 {
		bucket := TrendBucket{From: time.UnixMilli(cursor).In(b.loc).Format(time.RFC3339), To: time.UnixMilli(b.next(cursor)).In(b.loc).Format(time.RFC3339)}
		if filled, ok := trend[cursor]; ok {
			bucket.Tokens, bucket.Records, bucket.EstimatedRecords, bucket.LegacyRecords = filled.Tokens, filled.Records, filled.EstimatedRecords, filled.LegacyRecords
		}
		out = append(out, bucket)
	}
	return out
}
