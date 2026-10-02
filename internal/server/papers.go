package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Every submission carries one paper as a PDF, stored as
// <data directory>/papers/<submission ID>.pdf.
const (
	maxPaperBytes      = 10 << 20              // 10 MB per PDF
	maxSubmissionBytes = maxPaperBytes + 1<<20 // the PDF plus the text fields

	// slowTransfer is how long one upload or paper download may take. The
	// server's own timeouts suit pages; a 10 MB PDF on a slow connection
	// needs longer.
	slowTransfer = 10 * time.Minute
)

// allowSlowTransfer extends the read and write deadlines of the current
// request to slowTransfer from now.
func allowSlowTransfer(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(slowTransfer)
	// An error only means the writer cannot change deadlines, as in tests
	// that use a ResponseRecorder.
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
}

// upload is a validated PDF waiting in the papers directory under a
// temporary name until its submission has an ID.
type upload struct {
	tmpPath string
	name    string // the file name on the author's computer, for reference
	size    int64
	sha256  string
}

func (s *Server) papersDir() string { return filepath.Join(s.store.Dir(), "papers") }

// receivePaper validates the "paper" file of a multipart submission and,
// when the rest of the form is valid too, copies it into the papers
// directory. Otherwise it returns a nil upload, with any problem with the
// file recorded on f. An error means the server could not store the file.
func (s *Server) receivePaper(r *http.Request, f *formState) (*upload, error) {
	file, hdr, err := r.FormFile("paper")
	switch {
	case errors.Is(err, http.ErrMissingFile), errors.Is(err, http.ErrNotMultipart):
		f.fail("paper", "Please upload your paper as a PDF file.")
		return nil, nil
	case err != nil:
		f.fail("paper", "The file could not be read. Please select it again.")
		return nil, nil
	}
	defer file.Close()

	limit := fmt.Sprintf("The PDF must be at most %d MB.", maxPaperBytes>>20)
	switch {
	case !strings.EqualFold(filepath.Ext(hdr.Filename), ".pdf"):
		f.fail("paper", "Please upload a PDF file (.pdf). Other formats are not accepted.")
		return nil, nil
	case hdr.Size > maxPaperBytes:
		f.fail("paper", limit)
		return nil, nil
	case !hasPDFSignature(file):
		f.fail("paper", "The file is not a valid PDF document.")
		return nil, nil
	case !f.ok():
		// Another field is wrong, so the form is shown again and the author
		// selects the file once more: there is nothing to keep yet.
		return nil, nil
	}

	if err := os.MkdirAll(s.papersDir(), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(s.papersDir(), ".upload-*.pdf")
	if err != nil {
		return nil, err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(file, maxPaperBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	if n > maxPaperBytes {
		os.Remove(tmp.Name())
		f.fail("paper", limit)
		return nil, nil
	}
	return &upload{tmpPath: tmp.Name(), name: cleanFileName(hdr.Filename), size: n, sha256: hex.EncodeToString(sum.Sum(nil))}, nil
}

// hasPDFSignature reports whether the file starts like a PDF: the "%PDF-"
// header must appear within the first 1024 bytes. The file is rewound.
// The browser-supplied content type is not trusted.
func hasPDFSignature(f multipart.File) bool {
	head := make([]byte, 1024)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	if !bytes.Contains(head[:n], []byte("%PDF-")) {
		return false
	}
	_, err = f.Seek(0, io.SeekStart)
	return err == nil
}

// cleanFileName keeps only the base name of an uploaded file, without
// control characters, for display to the organizers.
func cleanFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	return truncate(name, 200)
}

// nextID returns the next free submission ID of a track, e.g.
// ICAS2027-W004 for base "ICAS2027-W". Numbers whose PDF is still on disk
// are skipped, so a record removed by hand never loses its file to a new
// submission.
func (s *Server) nextID(subs []Submission, track, base string) string {
	for n := nextNumber(subs, track); ; n++ {
		id := fmt.Sprintf("%s%03d", base, n)
		if _, err := os.Lstat(filepath.Join(s.papersDir(), id+".pdf")); err != nil {
			return id
		}
	}
}

// nextNumber returns one more than the highest number already used in the
// track, so IDs (and PDF names) stay unique even if records are removed.
func nextNumber(subs []Submission, track string) int {
	n := 0
	for _, e := range subs {
		if e.Track != track {
			continue
		}
		i := strings.LastIndexAny(e.ID, "WP")
		if i < 0 {
			continue
		}
		if v, err := strconv.Atoi(e.ID[i+1:]); err == nil && v > n {
			n = v
		}
	}
	return n + 1
}

// handleAdminPaper sends one uploaded PDF to an authenticated organizer.
func (s *Server) handleAdminPaper(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	allowSlowTransfer(w)
	id := r.URL.Query().Get("id")
	if !receiptPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(s.papersDir(), id+".pdf"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.serverError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", `attachment; filename="`+id+`.pdf"`)
	h.Set("Cache-Control", "no-store")
	http.ServeContent(w, r, id+".pdf", info.ModTime(), f)
}

// handleAdminPapersZip bundles every uploaded PDF in one archive. Files are
// named by submission ID only, so the archive can be given to reviewers
// together with the anonymized CSV export.
func (s *Server) handleAdminPapersZip(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	allowSlowTransfer(w)
	d, err := s.loadAdminData()
	if err != nil {
		s.serverError(w, err)
		return
	}
	subs := append([]Submission(nil), d.Submissions...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].ID < subs[j].ID })

	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="icas-papers-%s.zip"`, s.now().Format("20060102")))
	h.Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	for _, sub := range subs {
		if sub.PaperFile == "" {
			continue
		}
		name := filepath.Base(sub.PaperFile)
		if err := addToZip(zw, filepath.Join(s.papersDir(), name), name, sub.Received); err != nil {
			s.logger.Printf("papers zip: %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		s.logger.Printf("papers zip: %v", err)
	}
}

func addToZip(zw *zip.Writer, path, name string, modified time.Time) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// PDFs are already compressed; storing them keeps the archive fast to build.
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: modified})
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
