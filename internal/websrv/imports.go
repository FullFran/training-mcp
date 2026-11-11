package websrv

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/fullfran/training-mcp/internal/importer"
	"github.com/fullfran/training-mcp/internal/training"
)

const (
	// maxImportRequest is the whole request: the file and the form around it.
	maxImportRequest = 1 << 20
	maxImportNames   = 1000
	importListLimit  = 20
)

// skipLabels name why rows were not imported, in the order the page lists them.
var skipLabels = []MemoryOption{
	{importer.SkipWarmup, "Calentamientos"},
	{importer.SkipNoLoad, "Sin peso o sin reps (cardio, peso corporal, tiempo)"},
	{importer.SkipNotASet, "Filas que no son series (descansos, notas)"},
	{importer.SkipUnreadable, "Filas ilegibles"},
}

// SkipCount is one reason rows were left out, and how many.
type SkipCount struct {
	Label string
	N     int
}

// ImportData is the import page: what was imported before and, while
// previewing, what this file would add.
type ImportData struct {
	History []training.ImportSummary
	Preview *importer.Preview
	Skipped []SkipCount
	// Payload carries the uploaded file, compressed, from the preview to the
	// confirmation, so the server keeps nothing between the two requests.
	Payload, FileName, Unit string
	Done                    string
}

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	s.renderImport(w, r, http.StatusOK, "", ImportData{})
}

func (s *Server) renderImport(w http.ResponseWriter, r *http.Request, status int, message string, in ImportData) {
	ctx := r.Context()
	history, err := s.service(ctx).ListImports(ctx, importListLimit)
	if err != nil {
		s.fail(w, err)
		return
	}
	in.History = history
	if in.Unit == "" {
		in.Unit = "kg"
	}
	data := pageData{Base: s.base, Today: s.todayDate(), Error: message, Import: in}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.render(w, r, "import.html", data)
}

// importInput is what a preview or a confirmation sent: the file and the
// person's choices about it.
type importInput struct {
	data           []byte
	fileName, unit string
	chosen         map[string]string
	refused        int
	refusedMessage string
}

// readImport reads the uploaded file, or the payload a preview handed back.
func (s *Server) readImport(w http.ResponseWriter, r *http.Request) importInput {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportRequest)
	in := importInput{chosen: map[string]string{}}
	err := r.ParseMultipartForm(maxImportRequest)
	if errors.Is(err, http.ErrNotMultipart) {
		err = r.ParseForm()
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return importInput{refused: http.StatusRequestEntityTooLarge, refusedMessage: tooLargeMessage}
	case err != nil:
		return importInput{refused: http.StatusBadRequest, refusedMessage: "No se pudo leer el formulario."}
	}
	in.unit = r.PostFormValue("unit")
	if file, header, err := r.FormFile("file"); err == nil {
		defer file.Close()
		in.fileName = header.Filename
		if in.data, err = io.ReadAll(io.LimitReader(file, importer.MaxBytes+1)); err != nil {
			return importInput{refused: http.StatusBadRequest, refusedMessage: "No se pudo leer el archivo."}
		}
	} else if payload := r.PostFormValue("payload"); payload != "" {
		in.fileName = r.PostFormValue("file_name")
		if in.data, err = unpack(payload); err != nil {
			return importInput{refused: http.StatusBadRequest, refusedMessage: "No se pudo recuperar el archivo. Vuelve a subirlo."}
		}
	} else {
		return importInput{refused: http.StatusBadRequest, refusedMessage: "Elige el archivo CSV exportado de Hevy o Strong."}
	}
	in.fileName = strings.TrimSpace(path.Base(strings.ReplaceAll(in.fileName, `\`, "/")))
	if len(in.fileName) > 200 {
		in.fileName = in.fileName[:200]
	}
	sources, targets := r.PostForm["src"], r.PostForm["dst"]
	if len(sources) != len(targets) || len(sources) > maxImportNames {
		return importInput{refused: http.StatusBadRequest, refusedMessage: "Revisa los nombres de los ejercicios."}
	}
	for i, src := range sources {
		in.chosen[src] = targets[i]
	}
	return in
}

const tooLargeMessage = "El archivo es demasiado grande para importarlo de una vez (máximo 960 KB). Exporta un periodo más corto y repite con el resto."

// parseImport reads the file, answering the person when it cannot.
func (s *Server) parseImport(w http.ResponseWriter, r *http.Request, in importInput) (importer.File, bool) {
	if in.refused != 0 {
		s.renderImport(w, r, in.refused, in.refusedMessage, ImportData{})
		return importer.File{}, false
	}
	f, err := importer.Parse(in.data, importer.Options{StrongUnit: in.unit})
	switch {
	case errors.Is(err, importer.ErrTooLarge):
		s.renderImport(w, r, http.StatusRequestEntityTooLarge, tooLargeMessage, ImportData{})
	case errors.Is(err, importer.ErrFormat):
		s.renderImport(w, r, http.StatusBadRequest, "Ese archivo no parece una exportación de entrenos de Hevy o Strong (CSV).", ImportData{})
	case err != nil:
		s.fail(w, err)
	default:
		return f, true
	}
	return importer.File{}, false
}

// previewImport shows what importing the file would add. It writes nothing.
func (s *Server) previewImport(w http.ResponseWriter, r *http.Request) {
	in := s.readImport(w, r)
	f, ok := s.parseImport(w, r, in)
	if !ok {
		return
	}
	ctx := r.Context()
	p, err := importer.Plan(ctx, s.service(ctx), f, in.chosen, in.fileName)
	if err != nil {
		s.fail(w, err)
		return
	}
	payload, err := pack(in.data)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.renderImport(w, r, http.StatusOK, "", ImportData{Preview: &p, Skipped: skipCounts(p.Skipped), Payload: payload, FileName: in.fileName, Unit: in.unit})
}

// applyImport is the person importing their own history: it applies at once,
// never as a proposal. Confirming the same file again adds nothing.
func (s *Server) applyImport(w http.ResponseWriter, r *http.Request) {
	in := s.readImport(w, r)
	f, ok := s.parseImport(w, r, in)
	if !ok {
		return
	}
	ctx := r.Context()
	_, summary, err := importer.Apply(ctx, s.service(ctx), f, in.chosen, in.fileName)
	switch {
	case errors.Is(err, training.ErrValidation):
		s.renderImport(w, r, http.StatusBadRequest, "Algún nombre de ejercicio es demasiado largo o está vacío.", ImportData{})
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	done := "No había nada nuevo que importar: esas series ya están en tu historial."
	if summary.Sets > 0 {
		done = fmt.Sprintf("%s %s en %s.", plural(summary.Sets, "Importada", "Importadas"), countOf(summary.Sets, "serie", "series"), countOf(summary.Sessions, "sesión", "sesiones"))
		if summary.Imputed > 0 {
			done += fmt.Sprintf(" %s sin RPE: guardadas con RPE 8 marcado como imputado.", countOf(summary.Imputed, "serie", "series"))
		}
	}
	s.renderImport(w, r, http.StatusOK, "", ImportData{Done: done})
}

func skipCounts(skipped map[string]int) []SkipCount {
	var out []SkipCount
	for _, l := range skipLabels {
		if n := skipped[l.Value]; n > 0 {
			out = append(out, SkipCount{Label: l.Label, N: n})
		}
	}
	return out
}

func countOf(n int, one, many string) string { return fmt.Sprintf("%d %s", n, plural(n, one, many)) }

// pack compresses the file for the preview form; unpack refuses anything that
// would not fit the limit uncompressed.
func pack(data []byte) (string, error) {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write(data); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b.Bytes()), nil
}

func unpack(payload string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, importer.MaxBytes+1))
}
