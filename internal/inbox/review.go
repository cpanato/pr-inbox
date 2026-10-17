package inbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cpanato/pr-inbox/internal/provider"
	"github.com/cpanato/pr-inbox/internal/state"
)

var ErrReviewUnavailable = errors.New("review submission is not available for this provider")
var ErrReviewConfirmation = errors.New("review confirmation expired or is invalid; preview the saved draft again")

type reviewConfirmation struct {
	id                                      int64
	revision, version, session, operationID string
	expires                                 time.Time
}
type ReviewPreview struct {
	Token     string    `json:"token"`
	Account   string    `json:"account"`
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	HeadSHA   string    `json:"headSHA"`
	Body      string    `json:"body"`
	Action    string    `json:"action"`
	ExpiresAt time.Time `json:"expiresAt"`
	Notices   []string  `json:"notices,omitempty"`
}

func (s *Service) ReviewsAvailable() bool {
	_, ok := s.provider.(provider.ReviewProvider)
	return ok && (s.Info().Kind == "github" || s.Info().Kind == "gitlab")
}
func (s *Service) reviewContext(id int64, revision string) (provider.ReviewProvider, provider.DiffSource, string, error) {
	if !s.ReviewsAvailable() {
		return nil, provider.DiffSource{}, "", ErrReviewUnavailable
	}
	source, err := s.inspected(id, revision)
	if err != nil {
		return nil, source, "", err
	}
	key, _, err := s.progressKey(id, revision)
	if err != nil {
		return nil, source, "", err
	}
	if s.reviewStore == nil {
		return nil, source, "", ErrStateUnavailable
	}
	return s.provider.(provider.ReviewProvider), source, key, nil
}
func (s *Service) ReviewOperation(id int64, revision string) (*state.ReviewOperation, error) {
	_, _, key, err := s.reviewContext(id, revision)
	if err != nil {
		return nil, err
	}
	return s.reviewStore.ReviewOperation(key)
}
func activeOperation(op *state.ReviewOperation) bool {
	return op != nil && (op.Status == "sending" || op.Status == "unknown")
}
func validReviewDraft(draft state.Draft) bool {
	return !draft.Deleted && draft.Version != "" && state.ValidDraft(draft.Body, draft.Action) && (draft.Action == "approve" || strings.TrimSpace(draft.Body) != "")
}
func (s *Service) PrepareReview(ctx context.Context, id int64, revision, version, session string) (ReviewPreview, error) {
	p, source, key, err := s.reviewContext(id, revision)
	if err != nil {
		return ReviewPreview{}, err
	}
	op, err := s.reviewStore.ReviewOperation(key)
	if err != nil {
		return ReviewPreview{}, err
	}
	if activeOperation(op) {
		return ReviewPreview{}, provider.ErrReviewUnknown
	}
	draft, err := s.reviewStore.ReadDraft(key, source.HeadSHA)
	if err != nil {
		return ReviewPreview{}, err
	}
	if draft.Version != version {
		return ReviewPreview{}, state.ErrDraftConflict
	}
	if !validReviewDraft(draft) {
		return ReviewPreview{}, provider.ErrInvalidReview
	}
	if err := p.ReviewPreflight(ctx, source, draft.Action); err != nil {
		return ReviewPreview{}, err
	}
	operationID := rand.Text()
	source.ReviewOperationID = operationID
	content := draft.Body
	var notices []string
	if formatter, ok := p.(provider.ReviewContentProvider); ok {
		content, err = formatter.ReviewContent(source, draft.Body, draft.Action)
		if err != nil {
			return ReviewPreview{}, err
		}
		notices = formatter.ReviewNotices(draft.Action)
	}
	preview := ReviewPreview{Token: rand.Text(), Account: s.Info().Viewer.Login, Repo: source.Repo, Number: source.Number, HeadSHA: source.HeadSHA, Body: content, Action: draft.Action, ExpiresAt: time.Now().Add(5 * time.Minute), Notices: notices}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.confirmations == nil {
		s.confirmations = map[string]reviewConfirmation{}
	}
	for token, entry := range s.confirmations {
		if time.Now().After(entry.expires) {
			delete(s.confirmations, token)
		}
	}
	if len(s.confirmations) >= 64 {
		for token := range s.confirmations {
			delete(s.confirmations, token)
			break
		}
	}
	s.confirmations[preview.Token] = reviewConfirmation{id: id, revision: revision, version: version, session: session, operationID: operationID, expires: preview.ExpiresAt}
	return preview, nil
}
func (s *Service) SubmitReview(ctx context.Context, id int64, revision, token, session string) (*state.ReviewOperation, error) {
	p, source, key, err := s.reviewContext(id, revision)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	confirmation, ok := s.confirmations[token]
	if ok && confirmation.session == session {
		delete(s.confirmations, token)
	}
	s.mu.Unlock()
	if !ok || confirmation.session != session || confirmation.id != id || confirmation.revision != revision || time.Now().After(confirmation.expires) {
		return nil, ErrReviewConfirmation
	}
	var result *state.ReviewOperation
	err = s.reviewStore.WithReview(ctx, key, func(tx *state.ReviewTransaction) error {
		previous, err := tx.Operation()
		if err != nil {
			return err
		}
		if activeOperation(previous) {
			return provider.ErrReviewUnknown
		}
		if previous != nil && previous.Status == "confirmed" && previous.DraftVersion == confirmation.version {
			result = previous
			return nil
		}
		draft, err := tx.Draft(source.HeadSHA)
		if err != nil {
			return err
		}
		if draft.Version != confirmation.version {
			return state.ErrDraftConflict
		}
		if !validReviewDraft(draft) {
			return provider.ErrInvalidReview
		}
		if err := p.ReviewPreflight(ctx, source, draft.Action); err != nil {
			return err
		}
		source.ReviewOperationID = confirmation.operationID
		content := draft.Body
		if formatter, ok := p.(provider.ReviewContentProvider); ok {
			content, err = formatter.ReviewContent(source, draft.Body, draft.Action)
			if err != nil {
				return err
			}
		}
		hash := sha256.Sum256([]byte(content))
		op := state.ReviewOperation{Owner: s.reviewOwner(), Target: &source.PullRequest, ProjectID: source.ProjectID, ID: confirmation.operationID, Status: "sending", HeadSHA: source.HeadSHA, DraftVersion: draft.Version, Action: draft.Action, BodyHash: hex.EncodeToString(hash[:]), StartedAt: time.Now().UTC()}
		if err := tx.Record(op); err != nil {
			return err
		} // No POST unless the operation is durable.
		review, sendErr := p.SubmitReview(ctx, source, content, draft.Action)
		if sendErr != nil {
			op.Result = review
			op.Status = "unknown"
			if errors.Is(sendErr, provider.ErrReviewRejected) {
				op.Status = "rejected"
			}
			if err := tx.Record(op); err != nil {
				return provider.ErrReviewUnknown
			}
			result = &op
			return sendErr
		}
		op.Status = "confirmed"
		op.Owner = s.reviewOwner()
		op.Target = &source.PullRequest
		op.ProjectID = source.ProjectID
		op.ConfirmedAt = time.Now().UTC()
		op.HeadStatus = "unknown"
		op.Result = review
		if err := tx.Record(op); err != nil {
			return provider.ErrReviewUnknown
		}
		finishReview(ctx, p, source, tx, &op)
		result = &op
		return nil
	})
	if result != nil && result.Status == "confirmed" {
		s.mu.Lock()
		s.cachedAt = time.Time{}
		s.reviewedAt = time.Time{}
		s.reviewedGeneration++
		s.mu.Unlock()
	}
	return result, err
}
func finishReview(ctx context.Context, p provider.ReviewProvider, source provider.DiffSource, tx *state.ReviewTransaction, op *state.ReviewOperation) {
	if !op.DraftCleared {
		cleared, err := tx.ClearDraft(op.HeadSHA, op.DraftVersion)
		op.DraftCleared = err == nil && cleared
	}
	head, err := p.ReviewHead(ctx, source)
	op.HeadStatus = "unknown"
	if err == nil {
		op.HeadStatus = "unchanged"
		if head != op.HeadSHA {
			op.HeadStatus = "changed"
		}
	}
	if err := tx.Record(*op); err != nil {
		op.DraftCleared = false
		op.HeadStatus = "unknown"
	}
}

// Reconciliation only reads the provider. No retry of the original POST occurs.
func (s *Service) ReconcileReview(ctx context.Context, id int64, revision string) (*state.ReviewOperation, error) {
	p, source, key, err := s.reviewContext(id, revision)
	if err != nil {
		return nil, err
	}
	var result *state.ReviewOperation
	err = s.reviewStore.WithReview(ctx, key, func(tx *state.ReviewTransaction) error {
		op, err := tx.Operation()
		if err != nil {
			return err
		}
		if op == nil {
			return ErrReviewConfirmation
		}
		if op.Status == "rejected" {
			result = op
			return nil
		}
		if op.Status != "confirmed" {
			review, err := p.FindReview(ctx, source, provider.ReviewMatch{OperationID: op.ID, Result: op.Result, HeadSHA: op.HeadSHA, Action: op.Action, BodyHash: op.BodyHash, StartedAt: op.StartedAt})
			if err != nil {
				if review.ID > 0 {
					op.Result = review
					if recordErr := tx.Record(*op); recordErr != nil {
						return provider.ErrReviewUnknown
					}
				}
				return err
			}
			op.Status = "confirmed"
			op.Owner = s.reviewOwner()
			op.Target = &source.PullRequest
			op.ProjectID = source.ProjectID
			op.ConfirmedAt = time.Now().UTC()
			op.HeadStatus = "unknown"
			op.Result = review
			if err := tx.Record(*op); err != nil {
				return provider.ErrReviewUnknown
			}
		}
		if !op.DraftCleared || op.HeadStatus == "unknown" {
			finishReview(ctx, p, source, tx, op)
		}
		result = op
		return nil
	})
	if result != nil && result.Status == "confirmed" {
		s.mu.Lock()
		s.cachedAt = time.Time{}
		s.reviewedAt = time.Time{}
		s.reviewedGeneration++
		s.mu.Unlock()
	}
	return result, err
}

func (s *Service) reviewOwner() string {
	info := s.Info()
	data, _ := json.Marshal([]any{info.Kind, info.Host, info.ID, info.Viewer.ID, info.Viewer.Login})
	return string(data)
}
