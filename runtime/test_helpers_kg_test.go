package runtime

import "context"

// neverRecallKG is a KnowledgeGraph that always declines recall and
// silently swallows ingest. Used in tests as a non-nil sentinel where the
// runtime should NOT actually invoke the KG (e.g., trivial messages,
// IngestSource="cron").
type neverRecallKG struct{}

func (neverRecallKG) ShouldRecall(string) bool                  { return false }
func (neverRecallKG) Recall(context.Context, string) string     { return "" }
func (neverRecallKG) Ingest(context.Context, []Message)         {}

// recallingKG is a controllable KG fake — tests can set Hint to inspect
// the dynamic-suffix injection and Ingested to verify thread capture.
type recallingKG struct {
	Hint     string
	Ingested []Message
}

func (k *recallingKG) ShouldRecall(string) bool              { return true }
func (k *recallingKG) Recall(context.Context, string) string { return k.Hint }
func (k *recallingKG) Ingest(_ context.Context, t []Message) {
	k.Ingested = append(k.Ingested, t...)
}
