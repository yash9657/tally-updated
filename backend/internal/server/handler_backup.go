package server

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// handleBackupCompany streams a gzipped JSON Lines archive of the whole company.
//
// ⛔ IT IS ITS OWN RESOURCE (`backup`), NOT A REPORT. Migration 0011 grants the
// seeded Data Entry level a coarse `allow display report`, so anything modelled
// as report:NAME is granted to every operator in every tenant on the day it
// ships. This route hands over every ledger, every party's details and every
// salary in one file; it emits Display/`backup`, a token no seeded level
// mentions, so it is default-DENIED for everyone except a level that says
// otherwise. That is why it needs no ACL migration — see routeAction.
//
// It is a Display and not a write, so it correctly does NOT inherit the WebAuthn
// step-up or the back-dated cutoff (both gate on Action.IsWrite()).
func (s *Server) handleBackupCompany(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}

	name, err := s.backup.CompanyName(ctx, companyID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// HEADERS BEFORE THE FIRST BYTE, because after one byte of body it is too
	// late to answer with an error status: the response is already 200 and a
	// failure mid-stream can only truncate. That truncation is exactly what the
	// archive's TRAILER exists to make detectable on the reading side, and the
	// handler logs the error so the server side is not silent about it either.
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename="+archiveFilename(name, time.Now()))
	// A backup is the most sensitive read in the product; it must not sit in a
	// shared cache on the way to the browser.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	if err := s.backup.Write(ctx, w, companyID); err != nil {
		// The status is already sent. Say so plainly rather than pretending.
		log.Printf("backup: company %s FAILED mid-stream, the downloaded archive is TRUNCATED "+
			"and its trailer will not match: %v", companyID, err)
		return
	}
}

// archiveFilename builds the download name.
//
// Tally names its backup TBK<gen>_<companycode>.001, which is a filename built
// for a world where the file went into a numbered directory beside the company.
// A downloaded file lands in one Downloads folder with everybody else's, so this
// one leads with the COMPANY NAME and the date — the two things a person needs
// to pick the right archive out of a list six months later — and keeps the
// extensions that say what the file actually is.
func archiveFilename(companyName string, at time.Time) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, companyName)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "company"
	}
	return fmt.Sprintf("%s-%s.tallybak.jsonl.gz", slug, at.Format("2006-01-02"))
}
