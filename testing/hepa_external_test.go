package testing

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/bluesky-social/indigo/api/flashes" // Register app.flashes.story lexicon type
	toolsozone "github.com/bluesky-social/indigo/api/ozone"
	"github.com/bluesky-social/indigo/plc"
)

// ExternalHEPA manages an external HEPA process for testing
type ExternalHEPA struct {
	cmd           *exec.Cmd
	ctx           context.Context
	cancel        context.CancelFunc
	mockOzone     *MockOzoneServer
	testPLC       *TestPLCServer
	logger        *slog.Logger
	relayHost     string
	spamImagePath string
	started       bool
}

func MustSetupExternalHEPA(t *testing.T, relayHost string, plcClient plc.PLCClient) *ExternalHEPA {
	ctx, cancel := context.WithCancel(context.Background())
	
	// Create mock ozone server
	mockOzone := NewMockOzoneServer(t)
	
	// Create test PLC server
	testPLC := NewTestPLCServer(t, plcClient)
	
	// Create logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	return &ExternalHEPA{
		ctx:           ctx,
		cancel:        cancel,
		mockOzone:     mockOzone,
		testPLC:       testPLC,
		logger:        logger,
		relayHost:     relayHost,
		spamImagePath: "",
		started:       false,
	}
}

func (h *ExternalHEPA) SetSpamImagePath(path string) {
	h.spamImagePath = path
}

func (h *ExternalHEPA) Start(t *testing.T) {
	// Build hepa binary from the project root
	hepaBinary := filepath.Join(os.TempDir(), "hepa-test")
	buildCmd := exec.Command("go", "build", "-o", hepaBinary, "../cmd/hepa")
	
	// Capture build output for debugging
	buildOutput, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build HEPA binary: %v\nBuild output: %s", err, string(buildOutput))
	}

	// Set up environment variables for HEPA
	env := []string{
		"ATP_RELAY_HOST=" + h.relayHost,
		"ATP_PLC_HOST=" + h.testPLC.Host(),
		"ATP_OZONE_HOST=" + h.mockOzone.Host(),
		"HEPA_OZONE_DID=did:plc:test-hepa-admin",
		"HEPA_OZONE_AUTH_ADMIN_TOKEN=test-token",
		"HEPA_COLLECTION_FILTER=app.flashes.",
		"HEPA_LOG_LEVEL=debug",
		"HEPA_FIREHOSE_PARALLELISM=1",
	}

	// Add spam detection config if spam image path is set
	if h.spamImagePath != "" {
		env = append(env, "HEPA_SPAM_IMAGE_PATH="+h.spamImagePath)
		env = append(env, "HEPA_SPAM_HASH_THRESHOLD=15")
	}

	// Launch HEPA as external process
	h.cmd = exec.CommandContext(h.ctx, hepaBinary, "run")
	h.cmd.Env = append(os.Environ(), env...)
	h.cmd.Stdout = os.Stdout
	h.cmd.Stderr = os.Stderr

	if err := h.cmd.Start(); err != nil {
		t.Fatalf("Failed to start external HEPA: %v", err)
	}

	h.started = true
	
	// Give HEPA time to start up and connect
	time.Sleep(2 * time.Second)
}

func (h *ExternalHEPA) Close() {
	if h.started && h.cmd != nil && h.cmd.Process != nil {
		h.cmd.Process.Kill()
		h.cmd.Wait()
	}
	h.cancel()
	h.mockOzone.Close()
	h.testPLC.Close()
}

func (h *ExternalHEPA) GetOzoneEvents() []toolsozone.ModerationEmitEvent_Input {
	return h.mockOzone.GetEvents()
}

func (h *ExternalHEPA) WaitForOzoneEvents(expectedCount int, timeout time.Duration) []toolsozone.ModerationEmitEvent_Input {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h.mockOzone.EventCount() >= expectedCount {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return h.GetOzoneEvents()
}

// TestExternalHEPASpamHashDetection tests spam image detection via perceptual hashing with app.flashes.story
func TestExternalHEPASpamHashDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping external HEPA integration test in 'short' test mode")
	}

	// Skip in CI environments that might not have the right build setup
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("skipping external HEPA integration test in CI environment")
	}

	// Setup test infrastructure
	didr := TestPLC(t)
	pds := MustSetupPDS(t, ".testpds", didr)
	pds.Run(t)
	defer pds.Cleanup()

	relay := MustSetupRelay(t, didr, true)
	relay.Run(t)

	// Configure relay to scrape from PDS
	relay.tr.TrialHosts = []string{pds.RawHost()}
	pds.RequestScraping(t, relay)
	pds.BumpLimits(t, relay)

	// Get absolute path to spam reference image for HEPA config
	spamRefPath, err := filepath.Abs("spam-also.jpg")
	if err != nil {
		t.Fatalf("Failed to get absolute path to spam-also.jpg: %v", err)
	}

	// Setup external HEPA with spam detection enabled
	hepa := MustSetupExternalHEPA(t, "ws://"+relay.Host(), didr)
	hepa.SetSpamImagePath(spamRefPath)
	hepa.Start(t)
	defer hepa.Close()

	// Create test user
	user := pds.MustNewUser(t, "testuser.testpds")

	// Test 1: Post non-spam image (should NOT trigger spam detection)
	t.Log("Posting non-spam image (spam-not.jpg)")
	user.PostStoryWithImage(t, "This is a clean story with a non-spam image", "spam-not.jpg")

	// Wait a bit for processing
	time.Sleep(2 * time.Second)

	// Verify no spam events were generated for the non-spam image
	events := hepa.GetOzoneEvents()
	if len(events) != 0 {
		t.Errorf("Expected no ozone events for non-spam image, got %d", len(events))
	}

	// Test 2: Post spam image (SHOULD trigger spam detection)
	t.Log("Posting spam image (spam-also.jpg)")
	user.PostStoryWithImage(t, "This is a story with a spam image", "spam-also.jpg")

	// Wait for processing and ozone notifications (label + tag + report = 3 events)
	events = hepa.WaitForOzoneEvents(3, 10*time.Second)

	// Verify at least 3 ozone events were generated (label + tag + report)
	if len(events) < 3 {
		t.Errorf("Expected at least 3 ozone events for spam image (label + tag + report), got %d", len(events))
		return
	}

	// Verify events are for spam image detection and from app.flashes.story collection
	labelFound := false
	tagFound := false
	reportFound := false

	for _, event := range events {
		// Verify event details
		if event.CreatedBy != "did:plc:test-hepa-admin" {
			t.Errorf("Expected event.CreatedBy to be did:plc:test-hepa-admin, got %s", event.CreatedBy)
		}

		if event.Event == nil {
			t.Error("Event.Event is nil")
			continue
		}

		// Verify the subject is from app.flashes.story collection
		if event.Subject == nil {
			t.Error("Event.Subject is nil")
			continue
		}

		if event.Subject.RepoStrongRef != nil {
			if !strings.Contains(event.Subject.RepoStrongRef.Uri, "app.flashes.story") {
				t.Errorf("Expected event subject to be from app.flashes.story collection, got: %s", event.Subject.RepoStrongRef.Uri)
			}
		}

		// Check for spam label event
		if event.Event.ModerationDefs_ModEventLabel != nil &&
			len(event.Event.ModerationDefs_ModEventLabel.CreateLabelVals) > 0 &&
			event.Event.ModerationDefs_ModEventLabel.CreateLabelVals[0] == "spam" {
			labelFound = true
			t.Log("✓ Found spam label event")
		}

		// Check for spam-image-detected tag event
		if event.Event.ModerationDefs_ModEventTag != nil &&
			len(event.Event.ModerationDefs_ModEventTag.Add) > 0 &&
			event.Event.ModerationDefs_ModEventTag.Add[0] == "spam-image-detected" {
			tagFound = true
			t.Log("✓ Found spam-image-detected tag event")
		}

		// Check for report event
		if event.Event.ModerationDefs_ModEventReport != nil {
			reportFound = true
			t.Log("✓ Found report event")
		}
	}

	if !labelFound {
		t.Error("Expected spam label event from spam image detection")
	}
	if !tagFound {
		t.Error("Expected spam-image-detected tag event from spam image detection")
	}
	if !reportFound {
		t.Error("Expected report event from spam image detection")
	}
}
