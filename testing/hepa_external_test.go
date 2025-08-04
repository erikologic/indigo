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

	toolsozone "github.com/bluesky-social/indigo/api/ozone"
	"github.com/bluesky-social/indigo/plc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ExternalHEPA manages an external HEPA process for testing
type ExternalHEPA struct {
	cmd       *exec.Cmd
	ctx       context.Context
	cancel    context.CancelFunc
	mockOzone *MockOzoneServer
	testPLC   *TestPLCServer
	logger    *slog.Logger
	relayHost string
	started   bool
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
		ctx:       ctx,
		cancel:    cancel,
		mockOzone: mockOzone,
		testPLC:   testPLC,
		logger:    logger,
		relayHost: relayHost,
		started:   false,
	}
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

// TestExternalHEPAIntegrationGTUBEPost tests that external HEPA service correctly filters and processes api.flashes events
func TestExternalHEPAIntegrationGTUBEPost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping external HEPA integration test in 'short' test mode")
	}
	
	// Skip in CI environments that might not have the right build setup
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("skipping external HEPA integration test in CI environment")
	}

	assert := assert.New(t)
	require := require.New(t)
	
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
	
	// Setup external HEPA to subscribe to relay
	hepa := MustSetupExternalHEPA(t, "ws://"+relay.Host(), didr)
	hepa.Start(t)
	defer hepa.Close()
	
	// Create test user and post content
	user := pds.MustNewUser(t, "testuser.testpds")
	
	// Post normal bsky content (should be filtered out)
	user.Post(t, "This is a normal bsky post with GTUBE: "+gtubeString)
	
	// Post GTUBE content in flash (should trigger automod)
	user.PostFlash(t, "Flash with GTUBE: "+gtubeString)
	
	// Wait for processing and ozone notifications
	events := hepa.WaitForOzoneEvents(2, 5*time.Second)
	
	// Verify exactly 2 ozone events were generated (label + tag for GTUBE flash only)
	require.Equal(2, len(events), "Expected exactly 2 ozone events for GTUBE flash (label + tag)")
	
	// Verify events are for spam detection and from flashes collection
	labelFound := false
	tagFound := false
	for _, event := range events {
		// Verify event details
		assert.Equal("did:plc:test-hepa-admin", event.CreatedBy)
		require.NotNil(event.Event)
		
		// Verify the subject is from flashes collection (record-level event)
		require.NotNil(event.Subject)
		if event.Subject.RepoStrongRef != nil {
			assert.True(strings.Contains(event.Subject.RepoStrongRef.Uri, "app.flashes.feed.post"), 
				"Expected event subject to be from flashes collection, got: %s", event.Subject.RepoStrongRef.Uri)
		}
		
		// Check for label event
		if event.Event.ModerationDefs_ModEventLabel != nil && 
		   len(event.Event.ModerationDefs_ModEventLabel.CreateLabelVals) > 0 &&
		   event.Event.ModerationDefs_ModEventLabel.CreateLabelVals[0] == "spam" {
			labelFound = true
		}
		
		// Check for tag event
		if event.Event.ModerationDefs_ModEventTag != nil &&
		   len(event.Event.ModerationDefs_ModEventTag.Add) > 0 &&
		   event.Event.ModerationDefs_ModEventTag.Add[0] == "gtube-flash" {
			tagFound = true
		}
	}
	
	assert.True(labelFound, "Expected spam label event from GTUBE flash")
	assert.True(tagFound, "Expected gtube-flash tag event from GTUBE flash")
}