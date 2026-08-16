package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	workspaceDirName = ".pipeline-workspace"
	summaryFileName  = ".pipeline-last-run-summary.json"
)

var testFuncPattern = regexp.MustCompile(`(?m)^func Test[^(]*\(`)

type ReviewArtifactsOptions struct {
	Root        string
	Task        string
	RunID       string
	FinalText   string
	MaxAttempts int
	Now         time.Time
}

type ReviewArtifactsResult struct {
	WorkspaceDir    string
	SummaryPath     string
	ExecutionReport ExecutionReportArtifact
	Assessment      FinalAssessmentArtifact
}

type ModuleInfo struct {
	Path              string   `json:"path"`
	Family            string   `json:"family"`
	SourceFiles       []string `json:"source_files"`
	TestFiles         []string `json:"test_files"`
	SourceFileCount   int      `json:"source_file_count"`
	TestFileCount     int      `json:"test_file_count"`
	TestFunctionCount int      `json:"test_function_count"`
	ReadmeReferenced  bool     `json:"readme_referenced"`
}

type TreeClassificationArtifact struct {
	GeneratedAt string       `json:"generated_at"`
	Root        string       `json:"root"`
	Task        string       `json:"task"`
	Modules     []ModuleInfo `json:"modules"`
}

type RubricCriterion struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Weight      int    `json:"weight"`
	Description string `json:"description"`
}

type FamilyRubric struct {
	ID          string            `json:"id"`
	Family      string            `json:"family"`
	Strict      bool              `json:"strict"`
	Description string            `json:"description"`
	Criteria    []RubricCriterion `json:"criteria"`
	TotalWeight int               `json:"total_weight"`
}

type TreeRubricsArtifact struct {
	GeneratedAt string         `json:"generated_at"`
	Strict      bool           `json:"strict"`
	Rubrics     []FamilyRubric `json:"rubrics"`
}

type TreeRubricVerificationArtifact struct {
	GeneratedAt     string   `json:"generated_at"`
	Strict          bool     `json:"strict"`
	Verified        bool     `json:"verified"`
	ModuleCount     int      `json:"module_count"`
	RubricCount     int      `json:"rubric_count"`
	MissingFamilies []string `json:"missing_families,omitempty"`
	Issues          []string `json:"issues,omitempty"`
}

type CriterionGrade struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Weight   int      `json:"weight"`
	Score    int      `json:"score"`
	Evidence []string `json:"evidence"`
}

type ModuleGrade struct {
	Path        string           `json:"path"`
	Family      string           `json:"family"`
	RubricID    string           `json:"rubric_id"`
	Score       int              `json:"score"`
	Verdict     string           `json:"verdict"`
	Evidence    []string         `json:"evidence"`
	Criteria    []CriterionGrade `json:"criteria"`
	SourceFiles int              `json:"source_files"`
	TestFiles   int              `json:"test_files"`
	TestFuncs   int              `json:"test_functions"`
}

type TreeGradingArtifact struct {
	GeneratedAt string        `json:"generated_at"`
	Strict      bool          `json:"strict"`
	Modules     []ModuleGrade `json:"modules"`
}

type QAReportArtifact struct {
	GeneratedAt      string   `json:"generated_at"`
	FailingModules   []string `json:"failing_modules"`
	WarningModules   []string `json:"warning_modules"`
	NoTestModules    []string `json:"no_test_modules"`
	PriorityActions  []string `json:"priority_actions"`
	StrictAssessment bool     `json:"strict_assessment"`
}

type DocReportArtifact struct {
	GeneratedAt string         `json:"generated_at"`
	Files       map[string]any `json:"files"`
}

type FinalAssessmentArtifact struct {
	GeneratedAt    string   `json:"generated_at"`
	Strict         bool     `json:"strict"`
	AggregateScore int      `json:"aggregate_score"`
	Verdict        string   `json:"verdict"`
	PassingModules []string `json:"passing_modules"`
	WarningModules []string `json:"warning_modules"`
	FailingModules []string `json:"failing_modules"`
	Summary        string   `json:"summary"`
	ReviewFinal    string   `json:"review_final,omitempty"`
}

type StageAttempt struct {
	Stage   string `json:"stage"`
	Attempt int    `json:"attempt"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

type ExecutionReportArtifact struct {
	Iteration      int            `json:"iteration"`
	Status         string         `json:"status"`
	Task           string         `json:"task"`
	RunID          string         `json:"run_id,omitempty"`
	RetryCount     int            `json:"retry_count"`
	Attempts       []StageAttempt `json:"attempts"`
	GeneratedFiles []string       `json:"generated_files"`
}

type ValidationReportArtifact struct {
	GeneratedAt       string   `json:"generated_at"`
	Passed            bool     `json:"passed"`
	ModuleCount       int      `json:"module_count"`
	GradedModuleCount int      `json:"graded_module_count"`
	Checks            []string `json:"checks"`
	Issues            []string `json:"issues,omitempty"`
}

type stageRecorder struct {
	attempts   []StageAttempt
	retryCount int
}

type pipelineSummary struct {
	Version         string                `json:"version"`
	Pipeline        string                `json:"pipeline"`
	Status          string                `json:"status"`
	Task            string                `json:"task"`
	ReviewedModules []string              `json:"reviewed_modules"`
	Verification    []map[string]string   `json:"verification"`
	FinalAssessment FinalAssessmentResult `json:"final_assessment"`
}

type FinalAssessmentResult struct {
	Verdict      string `json:"verdict"`
	TreeGrading  string `json:"tree_grading"`
	Score        int    `json:"score"`
	StrictReview bool   `json:"strict_review"`
}

func WriteReviewArtifacts(ctx context.Context, opts ReviewArtifactsOptions) (ReviewArtifactsResult, error) {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return ReviewArtifactsResult{}, err
	}
	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 2
	}
	workspaceDir := filepath.Join(root, workspaceDirName)
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return ReviewArtifactsResult{}, fmt.Errorf("create pipeline workspace: %w", err)
	}

	readme := readOptional(filepath.Join(root, "README.md"))
	modules, err := discoverModules(root, readme)
	if err != nil {
		return ReviewArtifactsResult{}, err
	}
	if len(modules) == 0 {
		return ReviewArtifactsResult{}, fmt.Errorf("no Go modules discovered under %s", root)
	}

	recorder := &stageRecorder{}
	classification, err := runStage(ctx, recorder, "tree-classification", maxAttempts, func(int) (TreeClassificationArtifact, error) {
		return TreeClassificationArtifact{
			GeneratedAt: now.Format(time.RFC3339),
			Root:        root,
			Task:        opts.Task,
			Modules:     modules,
		}, nil
	}, func(artifact TreeClassificationArtifact) error {
		if len(artifact.Modules) == 0 {
			return fmt.Errorf("classification has no modules")
		}
		return nil
	})
	if err != nil {
		return ReviewArtifactsResult{}, err
	}

	baseRubrics, err := runStage(ctx, recorder, "tree-rubrics", maxAttempts, func(int) (TreeRubricsArtifact, error) {
		return TreeRubricsArtifact{
			GeneratedAt: now.Format(time.RFC3339),
			Strict:      true,
			Rubrics:     buildRubrics(classification.Modules),
		}, nil
	}, func(artifact TreeRubricsArtifact) error {
		if len(artifact.Rubrics) == 0 {
			return fmt.Errorf("no rubrics generated")
		}
		return nil
	})
	if err != nil {
		return ReviewArtifactsResult{}, err
	}

	verification, err := runStage(ctx, recorder, "tree-rubric-verification", maxAttempts, func(int) (TreeRubricVerificationArtifact, error) {
		return verifyRubrics(now, classification.Modules, baseRubrics.Rubrics), nil
	}, func(artifact TreeRubricVerificationArtifact) error {
		if artifact.Verified {
			return nil
		}
		return fmt.Errorf("%s", strings.Join(artifact.Issues, "; "))
	})
	if err != nil {
		verification = verifyRubrics(now, classification.Modules, baseRubrics.Rubrics)
	}

	refined, err := runStage(ctx, recorder, "tree-rubrics-refined", maxAttempts, func(int) (TreeRubricsArtifact, error) {
		return TreeRubricsArtifact{
			GeneratedAt: now.Format(time.RFC3339),
			Strict:      true,
			Rubrics:     refineRubrics(classification.Modules, baseRubrics.Rubrics, verification),
		}, nil
	}, func(artifact TreeRubricsArtifact) error {
		verified := verifyRubrics(now, classification.Modules, artifact.Rubrics)
		if verified.Verified {
			return nil
		}
		return fmt.Errorf("%s", strings.Join(verified.Issues, "; "))
	})
	if err != nil {
		return ReviewArtifactsResult{}, err
	}

	grading, err := runStage(ctx, recorder, "tree-grading-individual", maxAttempts, func(int) (TreeGradingArtifact, error) {
		return TreeGradingArtifact{
			GeneratedAt: now.Format(time.RFC3339),
			Strict:      true,
			Modules:     gradeModules(classification.Modules, refined.Rubrics),
		}, nil
	}, func(artifact TreeGradingArtifact) error {
		return validateGrades(classification.Modules, artifact.Modules)
	})
	if err != nil {
		return ReviewArtifactsResult{}, err
	}

	qa := buildQAReport(now, grading.Modules)
	doc := buildDocReport(now, root)
	assessment := buildFinalAssessment(now, grading.Modules, opts.FinalText)
	validation := buildValidationReport(now, classification.Modules, refined.Rubrics, grading.Modules, assessment)

	design := buildDesignMarkdown(now, opts.Task, classification.Modules, assessment)
	spec := map[string]any{
		"task":           opts.Task,
		"mode":           "streamma-review",
		"run_id":         opts.RunID,
		"workspace_root": root,
		"strict":         true,
		"generated_at":   now.Format(time.RFC3339),
	}
	plan := map[string]any{
		"task": opts.Task,
		"stages": []string{
			"tree-classification",
			"tree-rubrics",
			"tree-rubric-verification",
			"tree-rubrics-refined",
			"tree-grading-individual",
			"final-assessment",
		},
		"success_criteria": []string{
			"Every discovered Go module is classified",
			"Every module family has a strict rubric with total weight 100",
			"Every module receives a strict score and verdict",
			"Failures are surfaced through retry-aware stage attempts",
		},
	}
	architecture := map[string]any{
		"module_count": len(classification.Modules),
		"families":     uniqueFamilies(classification.Modules),
		"modules":      classification.Modules,
	}
	dispatch := map[string]any{
		"entrypoint": "streamma review当前项目",
		"agent_graph": []string{
			"scout",
			"critic",
			"finalizer",
		},
		"artifact_stages": []string{
			"tree-classification",
			"tree-rubrics",
			"tree-rubric-verification",
			"tree-rubrics-refined",
			"tree-grading-individual",
			"final-assessment",
		},
		"strict": true,
	}
	merge := map[string]any{
		"status": "not_applicable",
		"reason": "review pipeline does not merge code changes",
	}

	generatedFiles := []string{
		filepath.Join(workspaceDir, "design.md"),
		filepath.Join(workspaceDir, "spec.json"),
		filepath.Join(workspaceDir, "plan.json"),
		filepath.Join(workspaceDir, "architecture.json"),
		filepath.Join(workspaceDir, "dispatch.json"),
		filepath.Join(workspaceDir, "execution-report.json"),
		filepath.Join(workspaceDir, "merge-report.json"),
		filepath.Join(workspaceDir, "validation-report.json"),
		filepath.Join(workspaceDir, "tree-classification.json"),
		filepath.Join(workspaceDir, "tree-rubrics.json"),
		filepath.Join(workspaceDir, "tree-rubric-verification.json"),
		filepath.Join(workspaceDir, "tree-rubrics-refined.json"),
		filepath.Join(workspaceDir, "tree-grading-individual.json"),
		filepath.Join(workspaceDir, "qa-report.json"),
		filepath.Join(workspaceDir, "doc-report.json"),
		filepath.Join(workspaceDir, "final-assessment.json"),
		filepath.Join(workspaceDir, "status.json"),
		filepath.Join(root, summaryFileName),
	}
	execution := ExecutionReportArtifact{
		Iteration:      1 + recorder.retryCount,
		Status:         "completed",
		Task:           opts.Task,
		RunID:          opts.RunID,
		RetryCount:     recorder.retryCount,
		Attempts:       append([]StageAttempt(nil), recorder.attempts...),
		GeneratedFiles: toSlashPaths(root, generatedFiles),
	}

	if err := writeTextFile(filepath.Join(workspaceDir, "design.md"), design); err != nil {
		return ReviewArtifactsResult{}, err
	}
	for path, payload := range map[string]any{
		filepath.Join(workspaceDir, "spec.json"):                     spec,
		filepath.Join(workspaceDir, "plan.json"):                     plan,
		filepath.Join(workspaceDir, "architecture.json"):             architecture,
		filepath.Join(workspaceDir, "dispatch.json"):                 dispatch,
		filepath.Join(workspaceDir, "execution-report.json"):         execution,
		filepath.Join(workspaceDir, "merge-report.json"):             merge,
		filepath.Join(workspaceDir, "validation-report.json"):        validation,
		filepath.Join(workspaceDir, "tree-classification.json"):      classification,
		filepath.Join(workspaceDir, "tree-rubrics.json"):             baseRubrics,
		filepath.Join(workspaceDir, "tree-rubric-verification.json"): verification,
		filepath.Join(workspaceDir, "tree-rubrics-refined.json"):     refined,
		filepath.Join(workspaceDir, "tree-grading-individual.json"):  grading,
		filepath.Join(workspaceDir, "qa-report.json"):                qa,
		filepath.Join(workspaceDir, "doc-report.json"):               doc,
		filepath.Join(workspaceDir, "final-assessment.json"):         assessment,
		filepath.Join(workspaceDir, "status.json"): map[string]any{
			"status":       "completed",
			"active":       false,
			"strict":       true,
			"generated_at": now.Format(time.RFC3339),
		},
	} {
		if err := writeJSONFile(path, payload); err != nil {
			return ReviewArtifactsResult{}, err
		}
	}

	summaryPath := filepath.Join(root, summaryFileName)
	summary := pipelineSummary{
		Version:         "1.0",
		Pipeline:        "multi-agent-pipeline",
		Status:          assessmentVerdictStatus(assessment.Verdict),
		Task:            opts.Task,
		ReviewedModules: modulePaths(grading.Modules),
		Verification: []map[string]string{
			{"artifact": "tree-classification.json", "status": "passed"},
			{"artifact": "tree-rubrics.json", "status": "passed"},
			{"artifact": "tree-grading-individual.json", "status": "passed"},
			{"artifact": "final-assessment.json", "status": "passed"},
		},
		FinalAssessment: FinalAssessmentResult{
			Verdict:      assessment.Verdict,
			TreeGrading:  "strict",
			Score:        assessment.AggregateScore,
			StrictReview: true,
		},
	}
	if err := writeJSONFile(summaryPath, summary); err != nil {
		return ReviewArtifactsResult{}, err
	}

	return ReviewArtifactsResult{
		WorkspaceDir:    workspaceDir,
		SummaryPath:     summaryPath,
		ExecutionReport: execution,
		Assessment:      assessment,
	}, nil
}

func runStage[T any](ctx context.Context, recorder *stageRecorder, stage string, maxAttempts int, produce func(attempt int) (T, error), validate func(T) error) (T, error) {
	var zero T
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		value, err := produce(attempt)
		if err == nil && validate != nil {
			err = validate(value)
		}
		status := "passed"
		if err != nil {
			status = "failed"
			lastErr = err
		}
		if recorder != nil {
			record := StageAttempt{Stage: stage, Attempt: attempt, Status: status}
			if err != nil {
				record.Error = err.Error()
			}
			recorder.attempts = append(recorder.attempts, record)
		}
		if err == nil {
			return value, nil
		}
		if recorder != nil && attempt < maxAttempts {
			recorder.retryCount++
		}
	}
	return zero, fmt.Errorf("%s failed after %d attempts: %w", stage, maxAttempts, lastErr)
}

func discoverModules(root, readme string) ([]ModuleInfo, error) {
	type accum struct {
		sourceFiles []string
		testFiles   []string
		testFuncs   int
	}
	modules := map[string]*accum{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." {
			return nil
		}
		acc := modules[dir]
		if acc == nil {
			acc = &accum{}
			modules[dir] = acc
		}
		if strings.HasSuffix(name, "_test.go") {
			acc.testFiles = append(acc.testFiles, rel)
			acc.testFuncs += countTestFunctions(filepath.Join(root, rel))
			return nil
		}
		acc.sourceFiles = append(acc.sourceFiles, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover modules: %w", err)
	}
	paths := make([]string, 0, len(modules))
	for dir, acc := range modules {
		if len(acc.sourceFiles) == 0 {
			continue
		}
		paths = append(paths, dir)
	}
	sort.Strings(paths)
	out := make([]ModuleInfo, 0, len(paths))
	for _, dir := range paths {
		acc := modules[dir]
		sort.Strings(acc.sourceFiles)
		sort.Strings(acc.testFiles)
		out = append(out, ModuleInfo{
			Path:              dir,
			Family:            moduleFamily(dir),
			SourceFiles:       acc.sourceFiles,
			TestFiles:         acc.testFiles,
			SourceFileCount:   len(acc.sourceFiles),
			TestFileCount:     len(acc.testFiles),
			TestFunctionCount: acc.testFuncs,
			ReadmeReferenced:  strings.Contains(readme, dir),
		})
	}
	return out, nil
}

func shouldSkipDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".paw", workspaceDirName, "vendor", "node_modules", "tmp", "temp", "users":
		return true
	default:
		return strings.HasPrefix(name, ".") && name != "."
	}
}

func countTestFunctions(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(testFuncPattern.FindAll(data, -1))
}

func moduleFamily(path string) string {
	path = filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(path, "cmd/"):
		return "cli"
	case strings.HasPrefix(path, "internal/streamma"):
		return "runtime"
	case strings.HasPrefix(path, "internal/loop"):
		return "orchestration"
	case strings.HasPrefix(path, "internal/task"):
		return "task"
	case strings.HasPrefix(path, "internal/ui"):
		return "ui"
	case strings.HasPrefix(path, "internal/tool"):
		return "tool"
	case strings.HasPrefix(path, "internal/model"):
		return "model"
	case strings.HasPrefix(path, "internal/session"):
		return "session"
	case strings.HasPrefix(path, "internal/settings"):
		return "settings"
	case strings.HasPrefix(path, "internal/message"):
		return "message"
	default:
		return "package"
	}
}

func buildRubrics(modules []ModuleInfo) []FamilyRubric {
	families := uniqueFamilies(modules)
	out := make([]FamilyRubric, 0, len(families))
	for _, family := range families {
		out = append(out, FamilyRubric{
			ID:          "rubric:" + family,
			Family:      family,
			Strict:      true,
			Description: familyDescription(family),
			Criteria: []RubricCriterion{
				{ID: "tests_present", Label: "Tests Present", Weight: 35, Description: "The module has at least one dedicated Go test file."},
				{ID: "test_function_density", Label: "Test Function Density", Weight: 25, Description: "The module has enough individual Test* cases relative to its source files."},
				{ID: "test_file_balance", Label: "Test Surface Balance", Weight: 20, Description: "The module exposes enough total test surface, combining dedicated test files and capped Test* case breadth, relative to its source files."},
				{ID: "readme_reference", Label: "README Reference", Weight: 10, Description: "The module or package path is referenced in README-level documentation."},
				{ID: "specialized_suite", Label: "Specialized Suite", Weight: 10, Description: "The module has more than one test file or enough targeted tests for a tiny package."},
			},
			TotalWeight: 100,
		})
	}
	return out
}

func verifyRubrics(now time.Time, modules []ModuleInfo, rubrics []FamilyRubric) TreeRubricVerificationArtifact {
	issues := []string{}
	seen := map[string]FamilyRubric{}
	for _, rubric := range rubrics {
		if _, exists := seen[rubric.Family]; exists {
			issues = append(issues, "duplicate rubric family: "+rubric.Family)
			continue
		}
		seen[rubric.Family] = rubric
		total := 0
		ids := map[string]bool{}
		for _, criterion := range rubric.Criteria {
			total += criterion.Weight
			if ids[criterion.ID] {
				issues = append(issues, "duplicate criterion id: "+rubric.Family+"/"+criterion.ID)
			}
			ids[criterion.ID] = true
		}
		if total != 100 {
			issues = append(issues, fmt.Sprintf("rubric weight total for %s = %d, want 100", rubric.Family, total))
		}
	}
	missing := []string{}
	for _, family := range uniqueFamilies(modules) {
		if _, ok := seen[family]; !ok {
			missing = append(missing, family)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		issues = append(issues, "missing rubric families: "+strings.Join(missing, ", "))
	}
	return TreeRubricVerificationArtifact{
		GeneratedAt:     now.Format(time.RFC3339),
		Strict:          true,
		Verified:        len(issues) == 0,
		ModuleCount:     len(modules),
		RubricCount:     len(rubrics),
		MissingFamilies: missing,
		Issues:          issues,
	}
}

func refineRubrics(modules []ModuleInfo, rubrics []FamilyRubric, verification TreeRubricVerificationArtifact) []FamilyRubric {
	byFamily := map[string]FamilyRubric{}
	for _, rubric := range rubrics {
		criteria := append([]RubricCriterion(nil), rubric.Criteria...)
		rubric.Criteria = criteria
		rubric.TotalWeight = normalizeWeights(criteria)
		byFamily[rubric.Family] = rubric
	}
	for _, family := range verification.MissingFamilies {
		for _, rubric := range buildRubrics([]ModuleInfo{{Family: family}}) {
			byFamily[family] = rubric
		}
	}
	families := uniqueFamilies(modules)
	out := make([]FamilyRubric, 0, len(families))
	for _, family := range families {
		out = append(out, byFamily[family])
	}
	return out
}

func normalizeWeights(criteria []RubricCriterion) int {
	total := 0
	for _, criterion := range criteria {
		total += criterion.Weight
	}
	if total == 100 || total == 0 {
		return total
	}
	remaining := 100
	for i := range criteria {
		if i == len(criteria)-1 {
			criteria[i].Weight = remaining
			break
		}
		weight := criteria[i].Weight * 100 / total
		criteria[i].Weight = weight
		remaining -= weight
	}
	return 100
}

func gradeModules(modules []ModuleInfo, rubrics []FamilyRubric) []ModuleGrade {
	byFamily := map[string]FamilyRubric{}
	for _, rubric := range rubrics {
		byFamily[rubric.Family] = rubric
	}
	out := make([]ModuleGrade, 0, len(modules))
	for _, module := range modules {
		rubric := byFamily[module.Family]
		criteria := []CriterionGrade{}
		score := 0
		for _, criterion := range rubric.Criteria {
			grade := CriterionGrade{
				ID:     criterion.ID,
				Label:  criterion.Label,
				Weight: criterion.Weight,
			}
			switch criterion.ID {
			case "tests_present":
				if module.TestFileCount > 0 {
					grade.Score = criterion.Weight
				}
				grade.Evidence = []string{fmt.Sprintf("test_files=%d", module.TestFileCount)}
			case "test_function_density":
				grade.Score = weightedRatio(criterion.Weight, module.TestFunctionCount, maxInt(module.SourceFileCount, 1))
				grade.Evidence = []string{fmt.Sprintf("test_functions=%d", module.TestFunctionCount), fmt.Sprintf("source_files=%d", module.SourceFileCount)}
			case "test_file_balance":
				surface := module.TestFileCount + min(module.TestFunctionCount, module.SourceFileCount)
				grade.Score = weightedRatio(criterion.Weight, surface, maxInt(module.SourceFileCount*2, 1))
				grade.Evidence = []string{
					fmt.Sprintf("test_files=%d", module.TestFileCount),
					fmt.Sprintf("test_functions=%d", module.TestFunctionCount),
					fmt.Sprintf("source_files=%d", module.SourceFileCount),
				}
			case "readme_reference":
				if module.ReadmeReferenced {
					grade.Score = criterion.Weight
				}
				grade.Evidence = []string{fmt.Sprintf("readme_referenced=%t", module.ReadmeReferenced)}
			case "specialized_suite":
				if module.TestFileCount > 1 || (module.SourceFileCount <= 1 && module.TestFileCount >= 1) || module.TestFunctionCount >= 3 {
					grade.Score = criterion.Weight
				}
				grade.Evidence = []string{fmt.Sprintf("test_files=%d", module.TestFileCount), fmt.Sprintf("test_functions=%d", module.TestFunctionCount)}
			}
			score += grade.Score
			criteria = append(criteria, grade)
		}
		out = append(out, ModuleGrade{
			Path:        module.Path,
			Family:      module.Family,
			RubricID:    rubric.ID,
			Score:       score,
			Verdict:     moduleVerdict(score),
			Evidence:    moduleEvidence(module),
			Criteria:    criteria,
			SourceFiles: module.SourceFileCount,
			TestFiles:   module.TestFileCount,
			TestFuncs:   module.TestFunctionCount,
		})
	}
	return out
}

func validateGrades(modules []ModuleInfo, grades []ModuleGrade) error {
	byPath := map[string]ModuleGrade{}
	for _, grade := range grades {
		byPath[grade.Path] = grade
		if grade.Score < 0 || grade.Score > 100 {
			return fmt.Errorf("grade %s score=%d out of range", grade.Path, grade.Score)
		}
	}
	for _, module := range modules {
		if _, ok := byPath[module.Path]; !ok {
			return fmt.Errorf("missing grade for %s", module.Path)
		}
	}
	return nil
}

func buildQAReport(now time.Time, grades []ModuleGrade) QAReportArtifact {
	failing := []string{}
	warning := []string{}
	noTests := []string{}
	for _, grade := range grades {
		if grade.TestFiles == 0 {
			noTests = append(noTests, grade.Path)
		}
		switch grade.Verdict {
		case "fail":
			failing = append(failing, grade.Path)
		case "warn":
			warning = append(warning, grade.Path)
		}
	}
	actions := []string{}
	if len(noTests) > 0 {
		actions = append(actions, "Add at least one _test.go file for modules with zero tests: "+strings.Join(noTests, ", "))
	}
	if len(failing) > 0 {
		actions = append(actions, "Raise failing module scores above 70 before considering the review pipeline accepted.")
	}
	if len(actions) == 0 {
		actions = append(actions, "No urgent QA follow-up required; all modules meet the strict threshold.")
	}
	return QAReportArtifact{
		GeneratedAt:      now.Format(time.RFC3339),
		FailingModules:   failing,
		WarningModules:   warning,
		NoTestModules:    noTests,
		PriorityActions:  actions,
		StrictAssessment: true,
	}
}

func buildDocReport(now time.Time, root string) DocReportArtifact {
	files := map[string]any{}
	for _, rel := range []string{"README.md", "CHANGELOG.md", ".env.local.example", ".gitignore"} {
		path := filepath.Join(root, rel)
		info, err := os.Stat(path)
		files[rel] = map[string]any{
			"exists": err == nil,
			"bytes":  int64(0),
		}
		if err == nil {
			files[rel] = map[string]any{
				"exists":        true,
				"bytes":         info.Size(),
				"last_modified": info.ModTime().UTC().Format(time.RFC3339),
			}
		}
	}
	return DocReportArtifact{
		GeneratedAt: now.Format(time.RFC3339),
		Files:       files,
	}
}

func buildFinalAssessment(now time.Time, grades []ModuleGrade, finalText string) FinalAssessmentArtifact {
	passing := []string{}
	warning := []string{}
	failing := []string{}
	total := 0
	for _, grade := range grades {
		total += grade.Score
		switch grade.Verdict {
		case "pass":
			passing = append(passing, grade.Path)
		case "warn":
			warning = append(warning, grade.Path)
		default:
			failing = append(failing, grade.Path)
		}
	}
	score := 0
	if len(grades) > 0 {
		score = total / len(grades)
	}
	verdict := "accept"
	if len(failing) > 0 || score < 70 {
		verdict = "reject"
	} else if len(warning) > 0 || score < 85 {
		verdict = "caution"
	}
	summary := fmt.Sprintf("Strict tree grading scored %d across %d modules; %d pass, %d warn, %d fail.", score, len(grades), len(passing), len(warning), len(failing))
	return FinalAssessmentArtifact{
		GeneratedAt:    now.Format(time.RFC3339),
		Strict:         true,
		AggregateScore: score,
		Verdict:        verdict,
		PassingModules: passing,
		WarningModules: warning,
		FailingModules: failing,
		Summary:        summary,
		ReviewFinal:    strings.TrimSpace(finalText),
	}
}

func buildValidationReport(now time.Time, modules []ModuleInfo, rubrics []FamilyRubric, grades []ModuleGrade, assessment FinalAssessmentArtifact) ValidationReportArtifact {
	checks := []string{
		fmt.Sprintf("classified_modules=%d", len(modules)),
		fmt.Sprintf("rubrics=%d", len(rubrics)),
		fmt.Sprintf("graded_modules=%d", len(grades)),
		fmt.Sprintf("aggregate_score=%d", assessment.AggregateScore),
	}
	issues := []string{}
	if len(modules) != len(grades) {
		issues = append(issues, "graded module count does not match classification count")
	}
	for _, rubric := range rubrics {
		if rubric.TotalWeight != 100 {
			issues = append(issues, "rubric total weight mismatch for "+rubric.Family)
		}
	}
	return ValidationReportArtifact{
		GeneratedAt:       now.Format(time.RFC3339),
		Passed:            len(issues) == 0,
		ModuleCount:       len(modules),
		GradedModuleCount: len(grades),
		Checks:            checks,
		Issues:            issues,
	}
}

func buildDesignMarkdown(now time.Time, task string, modules []ModuleInfo, assessment FinalAssessmentArtifact) string {
	var builder strings.Builder
	builder.WriteString("# Review Pipeline\n\n")
	builder.WriteString("- Generated: " + now.Format(time.RFC3339) + "\n")
	builder.WriteString("- Task: " + strings.TrimSpace(task) + "\n")
	builder.WriteString("- Modules: " + fmt.Sprintf("%d", len(modules)) + "\n")
	builder.WriteString("- Aggregate Score: " + fmt.Sprintf("%d", assessment.AggregateScore) + "\n")
	builder.WriteString("- Verdict: " + assessment.Verdict + "\n")
	if assessment.ReviewFinal != "" {
		builder.WriteString("\n## Review Summary\n\n")
		builder.WriteString(assessment.ReviewFinal)
		builder.WriteString("\n")
	}
	return builder.String()
}

func familyDescription(family string) string {
	switch family {
	case "runtime":
		return "Strict grading for StreamMA/runtime packages."
	case "orchestration":
		return "Strict grading for loop and orchestration packages."
	case "ui":
		return "Strict grading for interactive UI packages."
	case "tool":
		return "Strict grading for tool execution packages."
	default:
		return "Strict grading for Go package quality."
	}
}

func uniqueFamilies(modules []ModuleInfo) []string {
	set := map[string]bool{}
	for _, module := range modules {
		set[module.Family] = true
	}
	out := make([]string, 0, len(set))
	for family := range set {
		out = append(out, family)
	}
	sort.Strings(out)
	return out
}

func modulePaths(grades []ModuleGrade) []string {
	paths := make([]string, 0, len(grades))
	for _, grade := range grades {
		paths = append(paths, grade.Path)
	}
	sort.Strings(paths)
	return paths
}

func moduleVerdict(score int) string {
	switch {
	case score >= 85:
		return "pass"
	case score >= 70:
		return "warn"
	default:
		return "fail"
	}
}

func moduleEvidence(module ModuleInfo) []string {
	return []string{
		fmt.Sprintf("source_files=%d", module.SourceFileCount),
		fmt.Sprintf("test_files=%d", module.TestFileCount),
		fmt.Sprintf("test_functions=%d", module.TestFunctionCount),
		fmt.Sprintf("readme_referenced=%t", module.ReadmeReferenced),
	}
}

func weightedRatio(weight, numerator, denominator int) int {
	if denominator <= 0 || numerator <= 0 || weight <= 0 {
		return 0
	}
	if numerator >= denominator {
		return weight
	}
	return numerator * weight / denominator
}

func resolveRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = cwd
	}
	return filepath.Abs(root)
}

func readOptional(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func writeJSONFile(path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeTextFile(path, text string) error {
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func toSlashPaths(root string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

func assessmentVerdictStatus(verdict string) string {
	switch verdict {
	case "accept":
		return "accepted"
	case "caution":
		return "warning"
	default:
		return "failed"
	}
}

func maxInt(a, b int) int {
	if b > a {
		return b
	}
	return a
}
