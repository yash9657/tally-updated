package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/backup"
	"github.com/khata/cored/internal/platform/db"
)

// maxArchiveBody bounds a restore upload.
//
// It is far larger than maxJSONBody and larger than maxXMLBody, because an
// archive is the whole company rather than one document, and refusing a real
// company's own backup at the door would make the feature undeliverable for
// exactly the users who need it most — the same lesson maxXMLBody's comment
// records about a genuine 84 MB TallyPrime export. The archive is GZIPPED, so
// this is a generous cap in compressed bytes; the restore streams the file
// rather than buffering it whole, and buffers only one table at a time.
const maxArchiveBody = 512 << 20

// handleRestoreCompany creates a company from an uploaded archive.
//
// ⛔ IT CREATES A NEW COMPANY AND NEVER REPLACES ONE, and the screen says so too.
// There is no DELETE /companies route anywhere and every *_no_mutate trigger is
// BEFORE UPDATE OR DELETE and RAISEs for EVERY role, so "restore over the
// existing company" is not a hard feature, it is an impossible one on this
// schema — and making it possible would mean putting a tenant-wipe into the
// product. When the archive's company is already here, this answers 409 with the
// reason rather than doing something surprising.
//
// AUTHN-ONLY, OUTSIDE THE {companyID} SUBTREE, so routeAction never sees it. See
// the route registration in routes() for why that is correct and what it means.
func (s *Server) handleRestoreCompany(w http.ResponseWriter, r *http.Request) {
	userID, ok := db.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no authenticated user")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArchiveBody)
	defer func() { _ = r.Body.Close() }()

	result, err := s.backup.Restore(r.Context(), r.Body, userID,
		func(ctx context.Context, tx pgx.Tx, companyID, uID uuid.UUID) error {
			return s.authzSeed.SeedCompanyAuthDefaultsInTx(ctx, tx, companyID, uID)
		})
	if err != nil {
		switch {
		case errors.Is(err, backup.ErrCompanyExists):
			writeError(w, http.StatusConflict, err.Error())
		default:
			// 422, not 500: every failure the restore reports is a statement about
			// the FILE — not an archive, a version this build cannot read, a
			// truncated download, a schema mismatch — and telling the operator
			// "internal server error" about their own file helps nobody.
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
