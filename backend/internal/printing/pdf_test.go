package printing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// reexecEnv makes the test binary emit the sample document's sha256 and exit,
// which is how TestTheSameDocumentPrintsTheSameBytesInAnotherProcess gets a
// genuinely separate process without shipping a helper main package.
const reexecEnv = "PRINTING_EMIT_SAMPLE_SHA"

// sampleDirEnv lets a human point the sample-file test at a directory outside the
// repo (the session scratchpad) so the produced PDF can be opened by hand and
// run through file(1). Unset, the test uses t.TempDir() and leaves nothing behind.
const sampleDirEnv = "PRINTING_SAMPLE_DIR"

func TestMain(m *testing.M) {
	if os.Getenv(reexecEnv) == "1" {
		pdf, err := sampleDoc().Bytes()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		sum := sha256.Sum256(pdf)
		fmt.Print(hex.EncodeToString(sum[:]))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// sampleDoc is the fixed two-page document every determinism assertion is made
// against. It deliberately contains the hostile description, an accented party
// name, a rupee amount, rules and a box.
func sampleDoc() *Doc {
	d := NewDoc(A4Portrait)

	p := d.NewPage()
	p.TextCenter(297.64, 800, HelveticaBold, 14, "TAX INVOICE")
	p.Text(40, 770, HelveticaBold, 11, "Mehta Trading Co.")
	p.Text(40, 756, Helvetica, 9, "27AAAPZ1234C1ZS")
	p.Text(40, 742, HelveticaOblique, 9, "Société Générale — Zürich")
	p.Rect(40, 400, 515, 320, 1)
	p.Line(40, 690, 555, 690, 0.5)
	p.Text(45, 675, Helvetica, 9, `Widget (Large) \ 50% "quoted"`)
	p.Text(45, 660, Courier, 9, "998519")
	p.TextRight(550, 660, HelveticaBold, 10, "₹1,23,456.78")

	p2 := d.NewPage()
	p2.Text(40, 800, Helvetica, 9, "continued on page 2")
	p2.TextRight(550, 100, CourierBold, 9, "Authorised Signatory")

	return d
}

// TestTheSameDocumentPrintsByteIdenticalOutputEveryTime is the foundation of every
// golden test in this track: if the bytes moved between two builds of the same
// document, a golden test could not tell a regression from a re-render.
func TestTheSameDocumentPrintsByteIdenticalOutputEveryTime(t *testing.T) {
	a, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	b, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("two renders of the same invoice differ (sha %x vs %x); no golden test on printed output can mean anything",
			sha256.Sum256(a), sha256.Sum256(b))
	}

	// Bytes() must also not mutate the Doc: calling it twice on ONE doc has to
	// return the same thing, or the second print of an invoice differs from the
	// first.
	d := sampleDoc()
	x, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	y, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !bytes.Equal(x, y) {
		t.Fatal("printing the same Doc twice produced different bytes, so Bytes() mutates the document")
	}
}

// TestTheSameDocumentPrintsTheSameBytesInAnotherProcess rules out the failure the
// in-process test cannot see: a map iteration order, an address-derived id or a
// per-process seed leaking into the output.
func TestTheSameDocumentPrintsTheSameBytesInAnotherProcess(t *testing.T) {
	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	want := sha256.Sum256(pdf)

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate the test binary to re-exec: %v", err)
	}
	cmd := exec.Command(self, "-test.run=TestMainReexecPlaceholder")
	cmd.Env = append(os.Environ(), reexecEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("re-exec failed: %v (stderr %q)", err, stderr.String())
	}
	got := strings.TrimSpace(string(out))
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("the same invoice rendered in a second process hashes to %s, in this one to %x; something process-local is leaking into the printed bytes",
			got, want)
	}
}

// TestPrintedBytesCarryNoTimestampSoAGoldenTestStaysValidTomorrow. A /CreationDate
// would change every byte after it, so an invoice reprinted a day later would not
// match its own golden file and a real regression would be invisible in the noise.
func TestPrintedBytesCarryNoTimestampSoAGoldenTestStaysValidTomorrow(t *testing.T) {
	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	s := string(pdf)
	for _, forbidden := range []string{"/CreationDate", "/ModDate", "/ID", "D:20"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("the printed document contains %q, which varies with the clock or the machine", forbidden)
		}
	}
	// A bare four-digit year is deliberately NOT checked: a 10-digit xref offset
	// can contain "2026" by arithmetic coincidence, and a flaky determinism test
	// is worse than none. These forms cannot occur by accident.
	now := time.Now()
	for _, stamp := range []string{
		now.Format("20060102"),
		now.Format("15:04"),
		now.Format("2006-01-02"),
		now.Format("02 Jan 2006"),
	} {
		if strings.Contains(s, stamp) {
			t.Errorf("the printed document contains the current-clock substring %q", stamp)
		}
	}
	// No compression: the point of an uncompressed stream is that a reviewer can
	// grep the file for the total that is supposed to be on it.
	if strings.Contains(s, "/Filter") || strings.Contains(s, "FlateDecode") {
		t.Error("a stream is compressed; the bytes are then not diffable and the output depends on the compressor version")
	}
	if !strings.Contains(s, "Rs.1,23,456.78") {
		t.Error("the invoice total cannot be found by grepping the file, so the streams are not the plain text this package promises")
	}
}

// TestATwoPageDocumentDeclaresBothPagesInThePageTree: a /Count that disagrees with
// /Kids gives a reader a document whose second page is unreachable — the page
// exists in the file and the customer never sees it.
func TestATwoPageDocumentDeclaresBothPagesInThePageTree(t *testing.T) {
	d := sampleDoc()
	if d.PageCount() != 2 {
		t.Fatalf("PageCount is %d, want 2", d.PageCount())
	}
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	s := string(pdf)
	if !strings.Contains(s, "/Type /Pages /Count 2 /Kids [10 0 R 12 0 R]") {
		t.Fatalf("the page tree is not the expected two-page tree; a reader that trusts /Count would show one page")
	}
	if n := strings.Count(s, "/Type /Page\n") + strings.Count(s, "/Type /Page "); n != 2 {
		t.Fatalf("found %d page objects, want 2", n)
	}
	lines, err := ExtractText(pdf)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	joined := strings.Join(lines, "|")
	if !strings.Contains(joined, "TAX INVOICE") {
		t.Error("page 1's heading is not in the extracted text")
	}
	if !strings.Contains(joined, "continued on page 2") {
		t.Error("page 2's text is not in the extracted text, so the second page carries nothing")
	}
	if !strings.Contains(joined, "Authorised Signatory") {
		t.Error("the signature block on page 2 is missing")
	}
}

// TestEveryXrefOffsetPointsAtTheObjectItClaims. An off-by-one xref offset produces
// a file that some readers repair silently and others refuse outright: the worst
// failure mode there is, because it passes a local eyeball check and fails at the
// customer.
func TestEveryXrefOffsetPointsAtTheObjectItClaims(t *testing.T) {
	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	entries, err := parseXref(pdf)
	if err != nil {
		t.Fatalf("our own xref table does not parse: %v", err)
	}
	if !entries[0].free || entries[0].gen != 65535 {
		t.Error("xref entry 0 is not the free-list head with generation 65535, which every reader checks first")
	}
	inUse := 0
	for n, e := range entries {
		if e.free {
			continue
		}
		inUse++
		want := []byte(strconv.Itoa(n) + " 0 obj\n")
		if e.offset < 0 || e.offset >= len(pdf) || !bytes.HasPrefix(pdf[e.offset:], want) {
			ctx := ""
			if e.offset >= 0 && e.offset < len(pdf) {
				end := e.offset + 20
				if end > len(pdf) {
					end = len(pdf)
				}
				ctx = string(pdf[e.offset:end])
			}
			t.Errorf("the xref says object %d starts at byte %d, but the file has %q there; this document is corrupt", n, e.offset, ctx)
		}
	}
	// 1 catalog + 1 pages + 5 fonts + 1 resources + 1 info + 2 pages x 2 = 13
	if inUse != 13 {
		t.Errorf("the xref lists %d in-use objects, want 13", inUse)
	}

	// /Size must be the entry count, including the free head.
	sizeRe := regexp.MustCompile(`/Size (\d+)`)
	m := sizeRe.FindStringSubmatch(string(pdf))
	if m == nil {
		t.Fatal("the trailer has no /Size")
	}
	if got, _ := strconv.Atoi(m[1]); got != len(entries) {
		t.Errorf("the trailer says /Size %d but the xref table has %d entries", got, len(entries))
	}

	// Every entry is exactly 20 bytes, so the table is randomly addressable.
	xi := bytes.LastIndex(pdf, []byte("\nxref\n"))
	ti := bytes.Index(pdf[xi:], []byte("trailer"))
	table := pdf[xi+len("\nxref\n") : xi+ti]
	header := bytes.IndexByte(table, '\n') + 1
	body := table[header:]
	if len(body)%20 != 0 {
		t.Errorf("the xref table body is %d bytes, which is not a whole number of 20-byte entries", len(body))
	}
}

// TestStartxrefPointsAtTheCrossReferenceTable: a reader opens a PDF by seeking to
// startxref FIRST. A wrong value there means the file does not open at all.
func TestStartxrefPointsAtTheCrossReferenceTable(t *testing.T) {
	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	i := bytes.LastIndex(pdf, []byte("startxref\n"))
	if i < 0 {
		t.Fatal("there is no startxref, so no reader can open the document")
	}
	rest := string(pdf[i+len("startxref\n"):])
	nl := strings.IndexByte(rest, '\n')
	off, err := strconv.Atoi(rest[:nl])
	if err != nil {
		t.Fatalf("startxref value %q is not a number", rest[:nl])
	}
	if !bytes.HasPrefix(pdf[off:], []byte("xref\n")) {
		t.Fatalf("startxref points at byte %d, where the file has %q instead of the xref keyword", off, pdf[off:off+10])
	}
}

// TestTheFileBeginsWithAPDFHeaderAndEndsWithEOF is the crudest possible check and
// still the one that catches a truncated write.
func TestTheFileBeginsWithAPDFHeaderAndEndsWithEOF(t *testing.T) {
	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) {
		t.Errorf("the file starts with %q, not a PDF 1.4 header", pdf[:min(12, len(pdf))])
	}
	if !bytes.HasSuffix(pdf, []byte("%%EOF")) {
		t.Errorf("the file ends with %q, not %%%%EOF", pdf[max(0, len(pdf)-12):])
	}
	// The recommended binary-marker comment: without it a transfer agent may
	// "fix" line endings and shift every xref offset.
	if !bytes.Contains(pdf[:32], []byte{'%', 0xE2, 0xE3, 0xCF, 0xD3}) {
		t.Error("the header has no high-byte comment, so a text-mode transfer would silently corrupt every xref offset")
	}
}

// TestAnEmptyDocumentIsRefusedRatherThanShippedBlank: a print handler that hit an
// empty voucher must get an error it can turn into a 4xx, not a file that opens
// grey.
func TestAnEmptyDocumentIsRefusedRatherThanShippedBlank(t *testing.T) {
	pdf, err := NewDoc(A4Portrait).Bytes()
	if !errors.Is(err, ErrNoPages) {
		t.Fatalf("an empty document returned err=%v, want ErrNoPages", err)
	}
	if pdf != nil {
		t.Fatalf("an empty document still produced %d bytes", len(pdf))
	}
}

// TestALandscapeReportGetsALandscapeMediaBox: a Trial Balance with eight columns
// is printed landscape, and the MediaBox is the only thing that tells the printer.
func TestALandscapeReportGetsALandscapeMediaBox(t *testing.T) {
	d := NewDoc(A4Landscape)
	p := d.NewPage()
	if p.Size().Width <= p.Size().Height {
		t.Fatal("A4Landscape is not wider than it is tall")
	}
	p.Text(20, 500, Helvetica, 9, "Trial Balance")
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !strings.Contains(string(pdf), "/MediaBox [0 0 841.89 595.28]") {
		t.Fatal("a landscape report was given a portrait MediaBox; the printer will crop the right-hand columns")
	}
}

// TestTheProducedFileIsWrittenToDiskAndOpensAsAPDF writes the sample out and
// re-reads it, because "it is a []byte in memory" is not the same claim as "a
// reader can open the file".
func TestTheProducedFileIsWrittenToDiskAndOpensAsAPDF(t *testing.T) {
	dir := os.Getenv(sampleDirEnv)
	if dir == "" {
		dir = t.TempDir()
	}
	path := filepath.Join(dir, "printing-sample.pdf")

	pdf, err := sampleDoc().Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if err := os.WriteFile(path, pdf, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back %s: %v", path, err)
	}
	if !bytes.Equal(back, pdf) {
		t.Fatal("the file on disk is not the bytes we produced")
	}
	if _, err := ExtractText(back); err != nil {
		t.Fatalf("the file written to %s does not parse as a document this package can read: %v", path, err)
	}
	t.Logf("wrote %d bytes to %s (sha256 %x)", len(back), path, sha256.Sum256(back))
}

// TestMainReexecPlaceholder exists so the re-exec above names a real test; the
// re-exec never reaches m.Run().
func TestMainReexecPlaceholder(t *testing.T) {}
