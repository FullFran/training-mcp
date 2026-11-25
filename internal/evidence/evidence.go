// Package evidence is the training-science library: a curated, versioned
// corpus of sources with their citation metadata and short summaries written in
// our own words, and the rules for searching it.
//
// The corpus is global, not per user, and read-only at runtime. It is embedded
// in the binary, so answering a question needs no network. A storage adapter
// indexes it (see Index) and the service rules here decide what a search
// means, so every driving adapter gets the same answer.
package evidence

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	// ErrInvalid is a query or record that breaks a rule of this package.
	ErrInvalid = errors.New("invalid evidence request")
	// ErrUnknownSource is a source id the loaded corpus does not have.
	ErrUnknownSource = errors.New("unknown evidence source")
)

// Review statuses. Only a named human expert may move a source past
// unreviewed; the tools always return the status so the coach can say it.
const (
	ReviewUnreviewed = "unreviewed"
	ReviewReviewed   = "reviewed"
)

// Summary bases: what the summary of a source was written from.
const (
	// SummaryFromAbstract: findings were written from the abstract of the
	// verified bibliographic record.
	SummaryFromAbstract = "abstract"
	// SummaryRecordOnly: no abstract was available; the summary says only
	// what the title and record support.
	SummaryRecordOnly = "record_only"
)

// Topic is one entry of the closed topic list. Terms are extra words, in
// English and Spanish, that the index adds to every source with the topic,
// so "descanso entre series" finds the rest-interval sources.
type Topic struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Terms string `json:"-"`
}

// Topics is the closed list a source's topics come from.
var Topics = []Topic{
	{ID: "volume", Label: "Training volume (weekly sets)", Terms: "volume weekly sets volumen series semanales"},
	{ID: "frequency", Label: "Training frequency", Terms: "frequency sessions per week frecuencia sesiones por semana"},
	{ID: "fractional_volume", Label: "Counting direct and indirect sets", Terms: "fractional volume direct indirect sets volumen fraccional series directas indirectas"},
	{ID: "proximity_to_failure", Label: "Proximity to failure", Terms: "proximity failure fallo muscular cerca del fallo"},
	{ID: "rir_rpe", Label: "RIR and RPE", Terms: "RIR RPE repetitions in reserve rating of perceived exertion repeticiones en reserva esfuerzo percibido"},
	{ID: "rest_intervals", Label: "Rest between sets", Terms: "rest interval inter-set descanso entre series pausa"},
	{ID: "autoregulation", Label: "Autoregulation", Terms: "autoregulation autoregulated autorregulación autorregulado"},
	{ID: "deload", Label: "Deloads", Terms: "deload deloading descarga semana de descarga"},
	{ID: "exercise_selection", Label: "Exercise selection and variation", Terms: "exercise selection variation selección de ejercicios variación"},
	{ID: "range_of_motion", Label: "Range of motion and muscle length", Terms: "range of motion ROM partial full muscle length rango de movimiento recorrido parcial completo longitud muscular"},
	{ID: "periodization", Label: "Periodization models", Terms: "periodization periodized linear undulating periodización periodizado lineal ondulante"},
	{ID: "load", Label: "Load and repetition range", Terms: "load intensity 1RM heavy light carga intensidad pesado ligero"},
	{ID: "hypertrophy", Label: "Muscle hypertrophy", Terms: "hypertrophy muscle growth muscle size hipertrofia crecimiento muscular"},
	{ID: "strength", Label: "Strength", Terms: "strength 1RM fuerza"},
	{ID: "fatigue", Label: "Fatigue and recovery", Terms: "fatigue recovery fatiga recuperación"},
}

// StudyTypes is the closed list a source's study type comes from.
var StudyTypes = []string{
	"meta_analysis", "systematic_review", "scoping_review", "randomized_trial", "controlled_trial",
	"acute_study", "narrative_review", "position_stand", "delphi_consensus", "cross_sectional_survey",
}

// Debate is a question the literature in the corpus disagrees on. Sources name
// the debates they take part in, so a search can show every side.
type Debate struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

// Verification records how a source was checked against a live bibliographic
// record before it entered the corpus.
type Verification struct {
	// Crossref and PubMed are the dates the record was checked there; empty
	// when not checked in that database (for example, a journal PubMed does
	// not index).
	Crossref        string `json:"crossref"`
	PubMed          string `json:"pubmed,omitempty"`
	RetractionCheck string `json:"retraction_check"`
}

// Source is one record of the corpus: a single publication.
type Source struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	Year        int      `json:"year"`
	Publication string   `json:"publication"`
	Volume      string   `json:"volume,omitempty"`
	Issue       string   `json:"issue,omitempty"`
	Pages       string   `json:"pages,omitempty"`
	StudyType   string   `json:"study_type"`
	DOI         string   `json:"doi,omitempty"`
	PMID        string   `json:"pmid,omitempty"`
	URL         string   `json:"url"`
	Population  string   `json:"population"`
	Topics      []string `json:"topics"`
	Debates     []string `json:"debates,omitempty"`
	// Keywords are extra search words that are not in the summary.
	Keywords       []string     `json:"keywords,omitempty"`
	KeyFindings    []string     `json:"key_findings"`
	Methods        string       `json:"methods"`
	Limitations    string       `json:"limitations"`
	SummaryBasis   string       `json:"summary_basis"`
	Verification   Verification `json:"verification"`
	IngestedOn     string       `json:"ingested_on"`
	ContentVersion int          `json:"content_version"`
	ReviewStatus   string       `json:"review_status"`
	// ReviewedBy and ReviewedOn name the human expert who reviewed the summary
	// and when; both are empty while the source is unreviewed.
	ReviewedBy string `json:"reviewed_by,omitempty"`
	ReviewedOn string `json:"reviewed_on,omitempty"`
}

// Citation is the source in one line, for quoting.
func (s Source) Citation() string {
	authors := strings.Join(s.Authors, ", ")
	if len(s.Authors) > 3 {
		authors = s.Authors[0] + " et al."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d). %s. %s", authors, s.Year, strings.TrimSuffix(s.Title, "."), s.Publication)
	if s.Volume != "" {
		fmt.Fprintf(&b, " %s", s.Volume)
		if s.Issue != "" {
			fmt.Fprintf(&b, "(%s)", s.Issue)
		}
	}
	if s.Pages != "" {
		fmt.Fprintf(&b, ":%s", s.Pages)
	}
	b.WriteString(".")
	if s.DOI != "" {
		fmt.Fprintf(&b, " doi:%s", s.DOI)
	}
	if s.PMID != "" {
		fmt.Fprintf(&b, " PMID:%s", s.PMID)
	}
	return b.String()
}

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	debatePattern = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)
	doiPattern    = regexp.MustCompile(`^10\.\d{4,9}/\S+$`)
	pmidPattern   = regexp.MustCompile(`^[1-9]\d{0,9}$`)
	datePattern   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// TopicIDs lists the ids of the closed topic list, in order.
func TopicIDs() []string {
	out := make([]string, len(Topics))
	for i, t := range Topics {
		out[i] = t.ID
	}
	return out
}

func knownTopic(id string) bool { return slices.Contains(TopicIDs(), id) }

func validDate(s string) bool {
	if !datePattern.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// Validate checks one record against the corpus rules. debates are the ids
// the corpus defines.
func (s Source) Validate(debates map[string]bool) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: source %q: %s", ErrInvalid, s.ID, fmt.Sprintf(format, args...))
	}
	switch {
	case !idPattern.MatchString(s.ID) || len(s.ID) > 80:
		return bad("id must be lower-case words joined by hyphens, at most 80 characters")
	case strings.TrimSpace(s.Title) == "":
		return bad("title is required")
	case len(s.Authors) == 0:
		return bad("at least one author is required")
	case s.Year < 1900 || s.Year > 2100:
		return bad("year %d is out of range", s.Year)
	case strings.TrimSpace(s.Publication) == "":
		return bad("publication is required")
	case !slices.Contains(StudyTypes, s.StudyType):
		return bad("study_type %q is not in the closed list", s.StudyType)
	case s.DOI == "" && s.PMID == "":
		return bad("a DOI or a PMID is required")
	case s.DOI != "" && !doiPattern.MatchString(s.DOI):
		return bad("doi %q is malformed", s.DOI)
	case s.PMID != "" && !pmidPattern.MatchString(s.PMID):
		return bad("pmid %q is malformed", s.PMID)
	case strings.TrimSpace(s.Population) == "":
		return bad("population is required")
	case len(s.Topics) == 0:
		return bad("at least one topic is required")
	case len(s.KeyFindings) == 0:
		return bad("at least one key finding is required")
	case strings.TrimSpace(s.Methods) == "" || strings.TrimSpace(s.Limitations) == "":
		return bad("methods and limitations are required")
	case s.SummaryBasis != SummaryFromAbstract && s.SummaryBasis != SummaryRecordOnly:
		return bad("summary_basis must be %q or %q", SummaryFromAbstract, SummaryRecordOnly)
	case !validDate(s.Verification.Crossref) && !validDate(s.Verification.PubMed):
		return bad("verification needs the date the record was checked in Crossref or PubMed")
	case s.Verification.PubMed != "" && !validDate(s.Verification.PubMed):
		return bad("verification.pubmed must be a YYYY-MM-DD date")
	case s.Verification.Crossref != "" && !validDate(s.Verification.Crossref):
		return bad("verification.crossref must be a YYYY-MM-DD date")
	case s.Verification.PubMed != "" && s.PMID == "":
		return bad("verified in PubMed but has no pmid")
	case strings.TrimSpace(s.Verification.RetractionCheck) == "":
		return bad("verification.retraction_check is required")
	case !validDate(s.IngestedOn):
		return bad("ingested_on must be a YYYY-MM-DD date")
	case s.ContentVersion < 1:
		return bad("content_version starts at 1")
	case s.ReviewStatus != ReviewUnreviewed && s.ReviewStatus != ReviewReviewed:
		return bad("review_status must be %q or %q", ReviewUnreviewed, ReviewReviewed)
	case s.ReviewStatus == ReviewReviewed && (strings.TrimSpace(s.ReviewedBy) == "" || !validDate(s.ReviewedOn)):
		return bad("a reviewed source names its reviewer and the review date")
	case s.ReviewStatus == ReviewUnreviewed && (s.ReviewedBy != "" || s.ReviewedOn != ""):
		return bad("an unreviewed source names no reviewer")
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return bad("url must be an absolute https URL")
	}
	seen := map[string]bool{}
	for _, t := range s.Topics {
		if !knownTopic(t) {
			return bad("topic %q is not in the closed list", t)
		}
		if seen[t] {
			return bad("topic %q is listed twice", t)
		}
		seen[t] = true
	}
	for _, d := range s.Debates {
		if !debates[d] {
			return bad("debate %q is not defined in the manifest", d)
		}
	}
	for _, f := range s.KeyFindings {
		if strings.TrimSpace(f) == "" {
			return bad("a key finding is empty")
		}
	}
	if s.SummaryBasis == SummaryRecordOnly && len(s.KeyFindings) != 1 {
		return bad("a record_only source states a single finding: what the record supports")
	}
	return nil
}
