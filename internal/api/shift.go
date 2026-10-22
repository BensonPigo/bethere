package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/BensonPigo/bethere/internal/domain"
	"github.com/BensonPigo/bethere/internal/store"
)

func (b body) shiftRequest() domain.ShiftRequest {
	return domain.ShiftRequest{FromCueID: b.fromCueID, DeltaMin: b.deltaMin, Excluded: b.excluded}
}

// shift applies a time shift (data-model.md 4.2) with the same rules as
// its preview.
func (a *API) shift(ctx context.Context, o store.TokenOwner, b body, now time.Time) (string, error) {
	_, _, err := a.store.ApplyCueOp(ctx, o.EventID, b.version, o.Actor, now, func(cues []domain.Cue) (domain.Op, error) {
		plan, err := domain.PlanShift(cues, b.shiftRequest(), now)
		return plan.Op, err
	})
	return "", err
}

// previewResponse is the shift preview (US-M-2.1); it writes nothing.
type previewResponse struct {
	Result  string      `json:"result"` // "ok"
	Preview previewView `json:"preview"`
}

type previewView struct {
	Changes   []cueMove      `json:"changes"`
	Conflicts []conflictView `json:"conflicts"`
	TooEarly  *tooEarlyView  `json:"tooEarly"`
}

type cueMove struct {
	CueID     int64  `json:"cueId"`
	FromStart string `json:"fromStart"`
	ToStart   string `json:"toStart"`
}

type tooEarlyView struct {
	CueID    int64  `json:"cueId"`
	NewStart string `json:"newStart"`
}

// previewShift runs the shift's rules on the caller's own cues without
// writing. The token, its event and the cues are read in the one read
// transaction of Snapshot (api.md section 7), and only the Master may ask.
func (a *API) previewShift(w http.ResponseWriter, r *http.Request) {
	hash, ok := bearerHash(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	now := a.now()
	d, err := a.store.Snapshot(ctx, hash, now)
	if err != nil {
		a.lookupFailed(w, r, err)
		return
	}
	a.seen(d.Owner, now)
	if !allowed(capShift, d.Owner.RoleType) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	b, ok := readBody(w, r, shiftFields(false))
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := d.Event.RequireLive(now); err != nil {
		a.writeFailed(w, r, err)
		return
	}
	cues := make([]domain.Cue, len(d.Cues))
	for i, c := range d.Cues {
		cues[i] = c.Cue
	}
	view := previewView{Changes: []cueMove{}, Conflicts: []conflictView{}}
	plan, err := domain.PlanShift(cues, b.shiftRequest(), now)
	var early *domain.ShiftTooEarlyError
	switch {
	case errors.As(err, &early):
		view.TooEarly = &tooEarlyView{CueID: early.CueID, NewStart: FormatTime(early.NewStart)}
	case err != nil:
		a.writeFailed(w, r, err)
		return
	default:
		for _, ch := range plan.Op.Changes {
			view.Changes = append(view.Changes, cueMove{
				CueID: ch.After.ID, FromStart: FormatTime(ch.Before.EffectiveStart()), ToStart: FormatTime(ch.After.EffectiveStart()),
			})
		}
		view.Conflicts = conflictViews(plan.Conflicts)
	}
	writeJSON(w, http.StatusOK, previewResponse{Result: "ok", Preview: view})
}
