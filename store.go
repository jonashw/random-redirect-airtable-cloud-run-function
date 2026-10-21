package function

import (
	"context"
	"errors"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jwilson4/go-tshirt/internal/airtableapi"
)

// snapshot is the cached copy of the active sites. Scans read only this.
type snapshot struct {
	Sites []airtableapi.Site `firestore:"sites"`
	// FetchedAt is when the Airtable read that produced Sites started. A
	// write with an older FetchedAt than the stored one is dropped, so two
	// overlapping refreshes can't leave older data in place.
	FetchedAt time.Time `firestore:"fetchedAt"`
	// WebhookCursor is where the next webhook payload read resumes.
	WebhookCursor int `firestore:"webhookCursor"`
}

var errNoSnapshot = errors.New("no snapshot stored yet")

type store interface {
	// Load returns errNoSnapshot if no sites have been saved yet; the
	// returned snapshot may still be non-nil and carry WebhookCursor.
	Load(ctx context.Context) (*snapshot, error)
	SaveSites(ctx context.Context, sites []airtableapi.Site, fetchedAt time.Time) error
	SaveCursor(ctx context.Context, cursor int) error
}

// firestoreStore keeps the snapshot in one document of the (default) database.
type firestoreStore struct {
	client *firestore.Client
	doc    *firestore.DocumentRef
}

func newFirestoreStore(ctx context.Context) (*firestoreStore, error) {
	client, err := firestore.NewClient(ctx, firestore.DetectProjectID)
	if err != nil {
		return nil, err
	}
	return &firestoreStore{client: client, doc: client.Collection("cache").Doc("sites")}, nil
}

func (s *firestoreStore) Load(ctx context.Context) (*snapshot, error) {
	ds, err := s.doc.Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, errNoSnapshot
	}
	if err != nil {
		return nil, err
	}
	var snap snapshot
	if err := ds.DataTo(&snap); err != nil {
		return nil, err
	}
	if snap.FetchedAt.IsZero() { // only a cursor has been written so far
		return &snap, errNoSnapshot
	}
	return &snap, nil
}

func (s *firestoreStore) SaveSites(ctx context.Context, sites []airtableapi.Site, fetchedAt time.Time) error {
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ds, err := tx.Get(s.doc)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		if err == nil {
			if prev, _ := ds.DataAt("fetchedAt"); prev != nil {
				if t, ok := prev.(time.Time); ok && t.After(fetchedAt) {
					return nil // a newer refresh already landed
				}
			}
		}
		if sites == nil {
			sites = []airtableapi.Site{}
		}
		return tx.Set(s.doc, map[string]any{"sites": sites, "fetchedAt": fetchedAt}, firestore.MergeAll)
	})
}

func (s *firestoreStore) SaveCursor(ctx context.Context, cursor int) error {
	_, err := s.doc.Set(ctx, map[string]any{"webhookCursor": cursor}, firestore.MergeAll)
	return err
}

// memoryStore is for local development (SNAPSHOT_STORE=memory) and tests.
type memoryStore struct {
	mu   sync.Mutex
	snap *snapshot
}

func (m *memoryStore) Load(context.Context) (*snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snap == nil {
		return nil, errNoSnapshot
	}
	cp := *m.snap
	if cp.FetchedAt.IsZero() {
		return &cp, errNoSnapshot
	}
	return &cp, nil
}

func (m *memoryStore) SaveSites(_ context.Context, sites []airtableapi.Site, fetchedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snap == nil {
		m.snap = &snapshot{}
	}
	if m.snap.FetchedAt.After(fetchedAt) {
		return nil
	}
	m.snap.Sites, m.snap.FetchedAt = sites, fetchedAt
	return nil
}

func (m *memoryStore) SaveCursor(_ context.Context, cursor int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snap == nil {
		m.snap = &snapshot{}
	}
	m.snap.WebhookCursor = cursor
	return nil
}

// SeedSnapshot writes sites to the Firestore snapshot without calling
// Airtable. It is for cmd/seedsnapshot, to fill the cache from a CSV export
// when the Airtable API is unavailable (e.g. the monthly call cap is spent).
func SeedSnapshot(ctx context.Context, sites []airtableapi.Site) error {
	s, err := newFirestoreStore(ctx)
	if err != nil {
		return err
	}
	return s.SaveSites(ctx, sites, time.Now())
}
