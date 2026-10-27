package tokenusage

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"maps"
	"slices"
	"time"

	"video-rename/internal/storage"
	"video-rename/internal/timebucket"
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

// Report is the token usage report.
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

// ReportRange is the time range of a report.
type ReportRange struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
	Interval string `json:"interval"`
}

// ReportTotals summarizes token usage in a report.
type ReportTotals struct {
	Tokens           int64 `json:"tokens"`
	Records          int64 `json:"records"`
	EstimatedRecords int64 `json:"estimated_records"`
	Batches          int64 `json:"batches"`
}

// TrendBucket holds the token usage of one time bucket.
type TrendBucket struct {
	From             string `json:"from"`
	To               string `json:"to"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
}

// ModelGroup is the token usage of one model.
type ModelGroup struct {
	Key              string `json:"key"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Protocol         string `json:"protocol"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
	Batches          int64  `json:"batches"`
}

// ProfileGroup is the token usage of one model profile.
type ProfileGroup struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
	Batches          int64  `json:"batches"`
}

// SourceGroup is the token usage of one batch source.
type SourceGroup struct {
	Key              string `json:"key"`
	Tokens           int64  `json:"tokens"`
	Records          int64  `json:"records"`
	EstimatedRecords int64  `json:"estimated_records"`
	Batches          int64  `json:"batches"`
}

// Batch is the token usage of one batch.
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
	LifetimeTokens   int64  `json:"lifetime_tokens"`
	LifetimeRecords  int64  `json:"lifetime_records"`
	LastRecordedAt   string `json:"last_recorded_at"`
}

// PagedBatches is a page of batches.
type PagedBatches struct {
	Items []Batch `json:"items"`
	Total int     `json:"total"`
	Page  int     `json:"page"`
	Size  int     `json:"size"`
}

// ReportOptions lists the values available as report filters.
type ReportOptions struct {
	Models   []OptionModel   `json:"models"`
	Profiles []OptionProfile `json:"profiles"`
}

// OptionModel is a model available as a filter.
type OptionModel struct {
	Key      string `json:"key"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	Protocol string `json:"protocol"`
}

// OptionProfile is a model profile available as a filter.
type OptionProfile struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	ModelKeys []string `json:"model_keys"`
	Deleted   bool     `json:"deleted"`
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
	id                                 int64
	meta                               runMeta
	status, created, started, finished string
	tokens, records, estimated, last   int64
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
func Analytics(ctx context.Context, db *storage.Store, q AnalyticsQuery) (*Report, error) {
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
	tx, err := db.Read().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	revision, err := storage.ReportRevision(ctx, tx, "usage")
	if err != nil {
		return nil, err
	}
	cacheQuery := q
	cacheQuery.Page, cacheQuery.Size, cacheQuery.Location = 0, 0, nil
	keyRaw, err := json.Marshal(struct {
		Query    AnalyticsQuery
		Timezone string
	}{cacheQuery, q.Location.String()})
	if err != nil {
		return nil, err
	}
	cacheKey := "usage:" + string(keyRaw)
	var cached Report
	if db.CachedReport(cacheKey, revision, &cached) {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return pageReport(ctx, &cached, q.Page, q.Size)
	}
	where := "r.recorded_at>=? AND r.recorded_at<?"
	args := []any{q.From, q.To}
	if q.Run > 0 {
		where += " AND r.run=?"
		args = append(args, q.Run)
	}
	if q.Profile > 0 {
		where += " AND COALESCE(r.model_config_id,u.model_config_id)=?"
		args = append(args, q.Profile)
	}
	if q.Source != "" {
		where += " AND (CASE WHEN u.source='schedule' OR u.plan>0 THEN 'scheduled' ELSE 'manual' END)=?"
		args = append(args, q.Source)
	}
	if q.Model != "" {
		where += " AND video_rename_model_key(COALESCE(r.config_summary,u.config_summary))=?"
		args = append(args, q.Model)
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.run,r.recorded_at,r.tokens,r.records,r.estimated_records,u.source,u.plan,COALESCE(r.config_summary,u.config_summary),COALESCE(r.model_config_id,u.model_config_id),COALESCE(r.model_config_title,u.model_config_title),u.status,u.created,u.started,u.finished,u.config_summary,u.model_config_id,u.model_config_title
FROM token_usage_records r JOIN runs u ON u.id=r.run
WHERE `+where+` ORDER BY r.recorded_at,r.id`, args...)
	if err != nil {
		return nil, err
	}
	buckets := timebucket.For(q.To-q.From, q.Location)
	trend := map[int64]*TrendBucket{}
	models := map[string]*ModelGroup{}
	profiles := map[int64]*ProfileGroup{}
	sources := map[string]*SourceGroup{}
	batches := map[int64]*batchAcc{}
	from := q.From
	first := true
	var totals ReportTotals
	modelBatches := map[string]map[int64]bool{}
	profileBatches := map[int64]map[int64]bool{}
	for rows.Next() {
		var run, recordedAt, tokens, records, estimated, plan, profileID int64
		var source, summary, profileTitle, status, created, started, finished, batchSummary, batchTitle string
		var batchProfile int64
		if err = rows.Scan(&run, &recordedAt, &tokens, &records, &estimated, &source, &plan, &summary, &profileID, &profileTitle, &status, &created, &started, &finished, &batchSummary, &batchProfile, &batchTitle); err != nil {
			rows.Close()
			return nil, err
		}
		meta := parseMeta(source, plan, summary, profileID, profileTitle)
		counted := records
		totals.Tokens += tokens
		totals.Records += counted
		totals.EstimatedRecords += estimated
		if first && q.From == 0 {
			buckets = timebucket.For(q.To-recordedAt, q.Location)
			from = buckets.Start(recordedAt)
		}
		first = false
		key := buckets.Start(recordedAt)
		bucket := trend[key]
		if bucket == nil {
			bucket = &TrendBucket{}
			trend[key] = bucket
		}
		bucket.Tokens += tokens
		bucket.Records += counted
		bucket.EstimatedRecords += estimated
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
			batch = &batchAcc{id: run, meta: parseMeta(source, plan, batchSummary, batchProfile, batchTitle), status: status, created: created, started: started, finished: finished}
			batches[run] = batch
			totals.Batches++
			origin.Batches++
		}
		if modelBatches[meta.key] == nil {
			modelBatches[meta.key] = map[int64]bool{}
		}
		if !modelBatches[meta.key][run] {
			model.Batches++
			modelBatches[meta.key][run] = true
		}
		if profileBatches[meta.profileID] == nil {
			profileBatches[meta.profileID] = map[int64]bool{}
		}
		if !profileBatches[meta.profileID][run] {
			profile.Batches++
			profileBatches[meta.profileID][run] = true
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
		batch.tokens += tokens
		batch.records += counted
		batch.estimated += estimated
		if recordedAt > batch.last {
			batch.last = recordedAt
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	options, err := reportOptions(ctx, db, tx)
	if err != nil {
		return nil, err
	}
	report := &Report{
		Range: ReportRange{
			From:     time.UnixMilli(from).In(q.Location).Format(time.RFC3339),
			To:       time.UnixMilli(q.To).In(q.Location).Format(time.RFC3339),
			Timezone: q.Location.String(),
			Interval: buckets.Name,
		},
		Totals:     totals,
		Trend:      trendSeries(buckets, from, q.To, trend),
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
	for i, acc := range sortedBatches(batches, "tokens") {
		if i == 10 {
			break
		}
		report.TopBatches = append(report.TopBatches, newBatch(acc))
		displayed[acc.id] = true
	}
	for i := range ordered {
		if !displayed[ordered[i].id] {
			displayed[ordered[i].id] = true
		}
		report.Batches.Items = append(report.Batches.Items, newBatch(ordered[i]))
	}
	lifetimes, err := lifetimeUsage(ctx, tx, displayed)
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
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	db.CacheReport(cacheKey, revision, report)
	return pageReport(ctx, report, q.Page, q.Size)
}

func newBatch(acc *batchAcc) Batch {
	return Batch{ID: acc.id, Model: acc.meta.model, Provider: acc.meta.provider, Protocol: acc.meta.protocol, ProfileID: acc.meta.profileID, ProfileTitle: acc.meta.profileTitle, Source: acc.meta.source, Status: acc.status, Created: acc.created, Started: acc.started, Finished: acc.finished, Tokens: acc.tokens, Records: acc.records, EstimatedRecords: acc.estimated, LastRecordedAt: time.UnixMilli(acc.last).UTC().Format(time.RFC3339)}
}

// lifetimeUsage reads the run aggregates for the displayed batches. They cover
// all recorded history, so a batch filtered to part of a range still shows its
// full lifetime next to the in-range numbers.
func lifetimeUsage(ctx context.Context, tx *sql.Tx, ids map[int64]bool) (map[int64][2]int64, error) {
	out := map[int64][2]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	keys := slices.Sorted(maps.Keys(ids))
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT run,token_count,token_records FROM run_token_usage WHERE run IN (SELECT value FROM json_each(?))", string(raw))
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
func reportOptions(ctx context.Context, db *storage.Store, tx *sql.Tx) (ReportOptions, error) {
	revision, err := storage.ReportRevision(ctx, tx, "usage_options")
	if err != nil {
		return ReportOptions{}, err
	}
	var cached ReportOptions
	if db.CachedReport("usage:options", revision, &cached) {
		return cached, nil
	}

	rows, err := tx.QueryContext(ctx, `SELECT u.source,u.plan,COALESCE(r.config_summary,u.config_summary),COALESCE(r.model_config_id,u.model_config_id),COALESCE(c.title,r.model_config_title,u.model_config_title),c.id IS NULL
FROM runs u JOIN token_usage_records r ON r.run=u.id
LEFT JOIN model_configs c ON c.id=COALESCE(r.model_config_id,u.model_config_id)
GROUP BY 1,2,3,4,5,6 ORDER BY MAX(r.recorded_at) DESC,MAX(r.id) DESC`)
	if err != nil {
		return ReportOptions{}, err
	}
	defer rows.Close()
	models := map[string]OptionModel{}
	profiles := map[int64]*OptionProfile{}
	for rows.Next() {
		var source, summary, title string
		var plan, profileID int64
		var deleted bool
		if err = rows.Scan(&source, &plan, &summary, &profileID, &title, &deleted); err != nil {
			return ReportOptions{}, err
		}
		meta := parseMeta(source, plan, summary, profileID, title)
		if _, ok := models[meta.key]; !ok {
			models[meta.key] = OptionModel{Key: meta.key, Model: meta.model, Provider: meta.provider, Protocol: meta.protocol}
		}
		profile := profiles[profileID]
		if profile == nil {
			// Existing configs use their current name; deleted configs use the
			// latest recorded name without losing their historical identity.
			profile = &OptionProfile{ID: profileID, Title: title, ModelKeys: []string{}, Deleted: deleted && profileID > 0}
			profiles[profileID] = profile
		}
		if !slices.Contains(profile.ModelKeys, meta.key) {
			profile.ModelKeys = append(profile.ModelKeys, meta.key)
		}
	}
	if err = rows.Err(); err != nil {
		return ReportOptions{}, err
	}
	out := ReportOptions{Models: slices.Collect(maps.Values(models)), Profiles: []OptionProfile{}}
	slices.SortFunc(out.Models, func(a, b OptionModel) int {
		if c := cmp.Compare(a.Model, b.Model); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	for profile := range maps.Values(profiles) {
		slices.Sort(profile.ModelKeys)
		out.Profiles = append(out.Profiles, *profile)
	}
	slices.SortFunc(out.Profiles, func(a, b OptionProfile) int { return cmp.Compare(a.ID, b.ID) })
	db.CacheReport("usage:options", revision, out)
	return out, nil
}

func sortedModels(models map[string]*ModelGroup) []ModelGroup {
	out := make([]ModelGroup, 0, len(models))
	for model := range maps.Values(models) {
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
	for profile := range maps.Values(profiles) {
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
	for source := range maps.Values(sources) {
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
	out := slices.Collect(maps.Values(batches))
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

// trendSeries renders the trend across the effective range, keeping zero
// buckets for quiet stretches instead of collapsing them.
func trendSeries(buckets timebucket.Bucketing, from, to int64, trend map[int64]*TrendBucket) []TrendBucket {
	out := []TrendBucket{}
	for start, end := range buckets.Spans(from, to) {
		bucket := TrendBucket{From: buckets.Format(start), To: buckets.Format(end)}
		if filled, ok := trend[start]; ok {
			bucket.Tokens, bucket.Records, bucket.EstimatedRecords = filled.Tokens, filled.Records, filled.EstimatedRecords
		}
		out = append(out, bucket)
	}
	return out
}

// The cached report includes the ordered batch rows; paging never re-reads items.
func pageReport(ctx context.Context, report *Report, page, size int) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := report.Batches.Items
	report.Batches.Page = min(page, max(1, (len(rows)+size-1)/size))
	report.Batches.Size = size
	start := (report.Batches.Page - 1) * size
	report.Batches.Items = rows[min(start, len(rows)):min(start+size, len(rows))]
	return report, nil
}
