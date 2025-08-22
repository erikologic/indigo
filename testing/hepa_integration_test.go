package testing

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	toolsozone "github.com/bluesky-social/indigo/api/ozone"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/automod"
	"github.com/bluesky-social/indigo/automod/cachestore"
	"github.com/bluesky-social/indigo/automod/consumer"
	"github.com/bluesky-social/indigo/automod/countstore"
	"github.com/bluesky-social/indigo/automod/engine"
	"github.com/bluesky-social/indigo/automod/flagstore"
	"github.com/bluesky-social/indigo/automod/rules"
	"github.com/bluesky-social/indigo/automod/setstore"
	"github.com/bluesky-social/indigo/automod/visual"
	"github.com/bluesky-social/indigo/plc"
	"github.com/bluesky-social/indigo/util"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

const gtubeString = "XJS*C4JDBQADN1.NSBN3*2IDNEN*GTUBE-STANDARD-ANTI-UBE-TEST-EMAIL*C.34X"

//go:embed kids.jpg
var kidsJpgData []byte

// MockCSAMServer simulates an external CSAM detection service for testing
type MockCSAMServer struct {
	server   *http.Server
	listener net.Listener
	// Configuration for which images should be detected as CSAM
	csamImages map[string]bool // maps image content hashes to CSAM status
}

func NewMockCSAMServer(t *testing.T) *MockCSAMServer {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	mock := &MockCSAMServer{
		listener:   listener,
		csamImages: make(map[string]bool),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/check-csam", mock.handleCheckCSAM)

	mock.server = &http.Server{
		Handler: mux,
	}

	go func() {
		if err := mock.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Errorf("Mock CSAM server error: %v", err)
		}
	}()

	return mock
}

func (m *MockCSAMServer) Host() string {
	return "http://" + m.listener.Addr().String()
}

func (m *MockCSAMServer) SetImageCSAM(imageCID string, isCSAM bool) {
	m.csamImages[imageCID] = isCSAM
}

func (m *MockCSAMServer) Close() {
	m.server.Close()
}

func (m *MockCSAMServer) handleCheckCSAM(w http.ResponseWriter, r *http.Request) {
	// Verify JWT token
	authHeader := r.Header.Get("Authorization")
	if authHeader != "Bearer test-csam-token" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse multipart form
	err := r.ParseMultipartForm(32 << 20) // 32MB
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "No image provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read image data
	imageData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read image", http.StatusBadRequest)
		return
	}

	// Simple hash of image content for testing
	imageHash := fmt.Sprintf("%x", sha256.Sum256(imageData))

	// Check if this image should be detected as CSAM
	isCSAM, exists := m.csamImages[imageHash]
	if !exists {
		isCSAM = false // Default to not CSAM
	}

	response := map[string]interface{}{
		"is_csam":    isCSAM,
		"confidence": 0.95,
		"message":    fmt.Sprintf("Mock detection result for hash %s", imageHash[:8]),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// MockOzoneServer captures ozone moderation events for testing
type MockOzoneServer struct {
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	events   []toolsozone.ModerationEmitEvent_Input
}

func NewMockOzoneServer(t *testing.T) *MockOzoneServer {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	mock := &MockOzoneServer{
		listener: listener,
		events:   []toolsozone.ModerationEmitEvent_Input{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/tools.ozone.moderation.emitEvent", mock.handleEmitEvent)
	mux.HandleFunc("/xrpc/tools.ozone.moderation.getRecord", mock.handleGetRecord)
	mux.HandleFunc("/xrpc/com.atproto.moderation.createReport", mock.handleCreateReport)
	mux.HandleFunc("/xrpc/tools.ozone.moderation.queryEvents", mock.handleQueryEvents)
	
	mock.server = &http.Server{Handler: mux}
	
	go func() {
		if err := mock.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Errorf("mock ozone server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(10 * time.Millisecond)
	
	return mock
}

func (m *MockOzoneServer) handleEmitEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var event toolsozone.ModerationEmitEvent_Input
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "failed to parse JSON", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()

	// Return successful response (matching ozone API format)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":        int64(len(m.events)),
		"createdAt": time.Now().Format(time.RFC3339),
		"createdBy": event.CreatedBy,
	})
}

func (m *MockOzoneServer) Host() string {
	return "http://" + m.listener.Addr().String()
}

func (m *MockOzoneServer) GetEvents() []toolsozone.ModerationEmitEvent_Input {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]toolsozone.ModerationEmitEvent_Input, len(m.events))
	copy(result, m.events)
	return result
}

func (m *MockOzoneServer) EventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func (m *MockOzoneServer) handleGetRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Return 404 for now - this simulates that the record isn't indexed in Ozone yet
	// This is the expected behavior according to the comment in persist.go:287
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   "RecordNotFound",
		"message": "Record not found in moderation system (simulated for testing)",
	})
}

func (m *MockOzoneServer) handleCreateReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Parse the report creation request
	var reportReq map[string]interface{}
	if err := json.Unmarshal(body, &reportReq); err != nil {
		http.Error(w, "failed to parse JSON", http.StatusBadRequest)
		return
	}

	// Return successful response (matching com.atproto.moderation.createReport output format)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]interface{}{
		"id":         int64(time.Now().UnixNano()), // Use nanosecond timestamp as unique ID
		"createdAt":  time.Now().Format(time.RFC3339),
		"reportedBy": "did:plc:test-hepa-admin", // From our HEPA admin
		"subject":    reportReq["subject"],
	}
	
	// Add optional fields if present
	if reasonType, ok := reportReq["reasonType"]; ok {
		response["reasonType"] = reasonType
	}
	if reason, ok := reportReq["reason"]; ok {
		response["reason"] = reason
	}
	
	json.NewEncoder(w).Encode(response)
}

func (m *MockOzoneServer) handleQueryEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Return empty events list for deduplication check
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"events":  []interface{}{}, // Empty list, no duplicate reports found
		"cursor":  "",
	})
}

func (m *MockOzoneServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

// FirehoseRunner interface for consumers that can run
type FirehoseRunner interface {
	Run(ctx context.Context) error
}

// TestHEPA manages a test HEPA instance
type TestHEPA struct {
	engine      *automod.Engine
	consumer    FirehoseRunner
	ctx         context.Context
	cancel      context.CancelFunc
	mockOzone   *MockOzoneServer
	testPLC     *TestPLCServer
	logger      *slog.Logger
	relayHost   string
}

// TestPLCServer wraps FakeDid to provide HTTP PLC endpoint for testing
type TestPLCServer struct {
	server   *http.Server
	listener net.Listener
	fakeDid  plc.PLCClient
}

func NewTestPLCServer(t *testing.T, fakeDid plc.PLCClient) *TestPLCServer {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	plcServer := &TestPLCServer{
		listener: listener,
		fakeDid:  fakeDid,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", plcServer.handleDIDLookup)
	
	plcServer.server = &http.Server{Handler: mux}
	
	go func() {
		if err := plcServer.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Errorf("test PLC server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(10 * time.Millisecond)
	
	return plcServer
}

func (p *TestPLCServer) handleDIDLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract DID from path (remove leading slash)
	didStr := strings.TrimPrefix(r.URL.Path, "/")
	if didStr == "" {
		http.Error(w, "missing DID", http.StatusBadRequest)
		return
	}

	doc, err := p.fakeDid.GetDocument(r.Context(), didStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("DID resolution failed: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (p *TestPLCServer) Host() string {
	return "http://" + p.listener.Addr().String()
}

func (p *TestPLCServer) Close() {
	if p.server != nil {
		p.server.Close()
	}
}

// createIdentityDirectory creates an identity directory for testing
func createIdentityDirectory(plcClient plc.PLCClient, plcURL string) identity.Directory {
	baseDir := identity.BaseDirectory{
		PLCURL: plcURL,
		HTTPClient: http.Client{
			Timeout: time.Second * 15,
		},
		PLCLimiter:            rate.NewLimiter(rate.Limit(100), 1),
		TryAuthoritativeDNS:   false, // Disable for testing
		SkipDNSDomainSuffixes: []string{},
	}
	
	cdir := identity.NewCacheDirectory(&baseDir, 1_500_000, time.Hour*24, time.Minute*2, time.Minute*5)
	return &cdir
}

func MustSetupHEPA(t *testing.T, relayHost string, plcClient plc.PLCClient) *TestHEPA {
	return MustSetupHEPAWithCSAM(t, relayHost, plcClient, nil, "")
}

func MustSetupHEPAWithCSAM(t *testing.T, relayHost string, plcClient plc.PLCClient, csamServer *MockCSAMServer, csamToken string) *TestHEPA {
	ctx, cancel := context.WithCancel(context.Background())
	
	// Create mock ozone server
	mockOzone := NewMockOzoneServer(t)
	
	// Create test PLC server
	testPLC := NewTestPLCServer(t, plcClient)
	
	// Create logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create identity directory from PLC client
	dir := createIdentityDirectory(plcClient, testPLC.Host())

	// Create ozone client pointing to mock server
	ozoneClient := &xrpc.Client{
		Client: util.RobustHTTPClient(),
		Host:   mockOzone.Host(),
		Auth: &xrpc.AuthInfo{
			Did: "did:plc:test-hepa-admin",
		},
	}

	// Create automod engine following HEPA's pattern
	counters := countstore.NewMemCountStore()
	cache := cachestore.NewMemCacheStore(5_000, 1*time.Hour)
	flags := flagstore.NewMemFlagStore()
	sets := setstore.NewMemSetStore()
	
	// Use default word sets (can load from JSON file in production)
	sets.Sets["bad-words"] = make(map[string]bool)
	sets.Sets["bad-words"]["hardestr"] = true
	sets.Sets["worst-words"] = make(map[string]bool) 
	sets.Sets["worst-words"]["hardestr"] = true
	
	bskyClient := xrpc.Client{
		Client: util.RobustHTTPClient(),
		Host:   "https://public.api.bsky.app",
	}

	// Setup ruleset with optional CSAM detection
	ruleset := rules.DefaultRules()
	if csamServer != nil && csamToken != "" {
		// Add CSAM detection rule
		csamClient := visual.NewCSAMClient(csamServer.Host(), csamToken)
		ruleset.BlobRules = append(ruleset.BlobRules, csamClient.CSAMDetectionBlobRule)
	}

	eng := &automod.Engine{
		Logger:      logger,
		Directory:   dir,
		Counters:    counters,
		Sets:        sets,
		Flags:       flags,
		Cache:       cache,
		Rules:       ruleset,
		BskyClient:  &bskyClient,
		OzoneClient: ozoneClient,
		BlobClient:  util.RobustHTTPClient(),
		Config: engine.EngineConfig{
			SkipAccountMeta:      true,
			ReportDupePeriod:     0, // Disable report deduplication for testing
			QuotaModReportDay:    10000,
			QuotaModTakedownDay:  200,
			QuotaModActionDay:    2000,
			RecordEventTimeout:   30 * time.Second,
			IdentityEventTimeout: 10 * time.Second,
			OzoneEventTimeout:    30 * time.Second,
		},
	}

	// Create firehose consumer with flashes-only filtering
	fc := &consumer.FirehoseConsumer{
		Engine:            eng,
		Logger:            logger.With("subsystem", "firehose-consumer"),
		Host:              relayHost,
		Parallelism:       1, // Use single worker for deterministic testing
		RedisClient:       nil,
		CollectionFilters: []string{"app.flashes."}, // Filter for app.flashes.* collections only
	}

	return &TestHEPA{
		engine:    eng,
		consumer:  fc,
		ctx:       ctx,
		cancel:    cancel,
		mockOzone: mockOzone,
		testPLC:   testPLC,
		logger:    logger,
		relayHost: relayHost,
	}
}

func (h *TestHEPA) Run(t *testing.T) {
	// Start firehose consumer in background
	go func() {
		if err := h.consumer.Run(h.ctx); err != nil && h.ctx.Err() == nil {
			t.Errorf("HEPA firehose consumer error: %v", err)
		}
	}()
	
	// Give HEPA time to connect and start processing
	time.Sleep(100 * time.Millisecond)
}

func (h *TestHEPA) Close() {
	h.cancel()
	h.mockOzone.Close()
	h.testPLC.Close()
}

func (h *TestHEPA) GetOzoneEvents() []toolsozone.ModerationEmitEvent_Input {
	return h.mockOzone.GetEvents()
}

func (h *TestHEPA) WaitForOzoneEvents(expectedCount int, timeout time.Duration) []toolsozone.ModerationEmitEvent_Input {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h.mockOzone.EventCount() >= expectedCount {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return h.GetOzoneEvents()
}

// TestHEPAIntegrationNormalPost tests that normal posts don't trigger ozone notifications
func TestHEPAIntegrationNormalPost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HEPA integration test in 'short' test mode")
	}

	assert := assert.New(t)
	
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
	
	// Setup HEPA to subscribe to relay
	hepa := MustSetupHEPA(t, "ws://"+relay.Host(), didr)
	hepa.Run(t)
	defer hepa.Close()
	
	// Create test user and post normal content
	user := pds.MustNewUser(t, "testuser.testpds")
	user.Post(t, "This is a normal post with no violations")
	
	// Wait a moment for processing
	time.Sleep(500 * time.Millisecond)
	
	// Verify no ozone events were generated
	events := hepa.GetOzoneEvents()
	assert.Equal(0, len(events), "Expected no ozone events for normal post, got %d", len(events))
}

// TestHEPAIntegrationGTUBEPost tests that GTUBE flashes trigger ozone notifications
func TestHEPAIntegrationGTUBEPost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HEPA integration test in 'short' test mode")
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
	
	// Setup HEPA to subscribe to relay
	hepa := MustSetupHEPA(t, "ws://"+relay.Host(), didr)
	hepa.Run(t)
	defer hepa.Close()
	
	// Create test user and post GTUBE content in flash (will trigger automod)
	user := pds.MustNewUser(t, "spammer.testpds")
	postRef := user.PostFlash(t, gtubeString)
	
	// Wait for processing and ozone notifications (GtubeFlashRule creates label + tag)
	events := hepa.WaitForOzoneEvents(2, 2*time.Second)
	
	// Verify ozone events were generated
	require.Equal(2, len(events), "Expected exactly 2 ozone events for GTUBE flash (label + tag)")
	
	// Verify events are for spam detection and from flashes collection
	labelFound := false
	tagFound := false
	for _, event := range events {
		// Verify event details
		assert.Equal("did:plc:test-hepa-admin", event.CreatedBy)
		require.NotNil(event.Event)
		
		// Verify the subject is correct (should be the flash record)
		require.NotNil(event.Subject)
		if event.Subject.RepoStrongRef != nil {
			// Record-level event
			assert.Equal(postRef.Uri, event.Subject.RepoStrongRef.Uri)
			assert.Equal(postRef.Cid, event.Subject.RepoStrongRef.Cid)
			assert.True(strings.Contains(event.Subject.RepoStrongRef.Uri, "app.flashes.feed.post"), 
				"Expected event subject to be from flashes collection, got: %s", event.Subject.RepoStrongRef.Uri)
		} else if event.Subject.AdminDefs_RepoRef != nil {
			// Account-level event
			assert.Equal(user.DID(), event.Subject.AdminDefs_RepoRef.Did)
		} else {
			t.Fatal("Expected either RepoStrongRef or AdminDefs_RepoRef in event subject")
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

// TestHEPAIntegrationMixedPosts tests both normal and violating posts together with flashes-only filtering
func TestHEPAIntegrationMixedPosts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HEPA integration test in 'short' test mode")
	}

	assert := assert.New(t)
	
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
	
	// Setup HEPA to subscribe to relay
	hepa := MustSetupHEPA(t, "ws://"+relay.Host(), didr)
	hepa.Run(t)
	defer hepa.Close()
	
	// Create test users
	normalUser := pds.MustNewUser(t, "normal.testpds")
	spammerUser := pds.MustNewUser(t, "spammer.testpds")
	
	// Post normal content (bsky posts - should be filtered out)
	normalUser.Post(t, "Hello world! This is a friendly post.")
	normalUser.Post(t, "Another normal post about cats and dogs.")
	
	// Post violating content in bsky (should be filtered out, even with GTUBE)
	spammerUser.Post(t, "Spam post with GTUBE: "+gtubeString)
	
	// Post more normal content (bsky posts - should be filtered out)
	normalUser.Post(t, "Yet another normal post.")
	
	// Post normal flashes content (should be processed but not trigger automod)
	normalUser.PostFlash(t, "This is a normal flash post")
	
	// Post violating flashes content (should trigger GTUBE detection)
	spammerUser.PostFlash(t, "Spam flash with GTUBE: "+gtubeString)
	
	// Wait for processing (GTUBE flash creates 2 events: label + tag)
	events := hepa.WaitForOzoneEvents(2, 2*time.Second)
	
	// Should have exactly 2 ozone events (only for the GTUBE flash: label + tag)
	assert.Equal(2, len(events), "Expected exactly 2 ozone events (only for GTUBE flash: label + tag)")
	
	// Verify events are for spam detection and from flashes collection
	labelFound := false
	tagFound := false
	for _, event := range events {
		// Verify the events are for flashes collection
		if event.Subject != nil && event.Subject.RepoStrongRef != nil {
			assert.True(strings.Contains(event.Subject.RepoStrongRef.Uri, "app.flashes.feed.post"), 
				"Expected event subject to be from flashes collection, got: %s", event.Subject.RepoStrongRef.Uri)
		}
		
		if event.Event.ModerationDefs_ModEventLabel != nil && 
		   len(event.Event.ModerationDefs_ModEventLabel.CreateLabelVals) > 0 &&
		   event.Event.ModerationDefs_ModEventLabel.CreateLabelVals[0] == "spam" {
			labelFound = true
		}
		if event.Event.ModerationDefs_ModEventTag != nil &&
		   len(event.Event.ModerationDefs_ModEventTag.Add) > 0 &&
		   event.Event.ModerationDefs_ModEventTag.Add[0] == "gtube-flash" {
			tagFound = true
		}
	}
	assert.True(labelFound, "Expected spam label event from GTUBE flash")
	assert.True(tagFound, "Expected gtube-flash tag event from GTUBE flash")
}

// TestHEPAIntegrationImageMatchKids tests that bsky posts with kids.jpg trigger CSAM detection

// Helper function to compute hash of embedded kids.jpg
func getKidsJpgHash() string {
	return fmt.Sprintf("%x", sha256.Sum256(kidsJpgData))
}

func TestHEPAIntegrationImageMatchKids(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HEPA integration test in 'short' test mode")
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
	
	// Create mock CSAM server and configure kids.jpg as CSAM
	csamServer := NewMockCSAMServer(t)
	defer csamServer.Close()
	
	// Configure kids.jpg to be detected as CSAM
	kidsHash := getKidsJpgHash()
	csamServer.SetImageCSAM(kidsHash, true)
	
	// Create test user first to get auth credentials
	user := pds.MustNewUser(t, "testuser.testpds")
	
	// Setup HEPA with CSAM detection service
	hepa := MustSetupHEPAWithCSAM(t, "ws://"+relay.Host(), didr, csamServer, "test-csam-token")
	// Override collection filter to allow flash posts
	hepa.consumer.(*consumer.FirehoseConsumer).CollectionFilters = []string{"app.flashes."}
	
	hepa.Run(t)
	defer hepa.Close()
	postRef := user.PostFlashWithImage(t, "This post contains kids.jpg", "kids.jpg")
	
	// Wait for processing and CSAM detection events
	events := hepa.WaitForOzoneEvents(3, 3*time.Second) // Expecting csam label + image-match-test tag + csam-detected account tag
	
	// Verify ozone events were generated
	require.GreaterOrEqual(len(events), 1, "Expected at least 1 ozone event for kids.jpg detection")
	
	// Verify events are for CSAM detection
	csamLabelFound := false
	imageTagFound := false
	for _, event := range events {
		// Verify event details
		assert.Equal("did:plc:test-hepa-admin", event.CreatedBy)
		require.NotNil(event.Event)
		
		// Verify the subject is correct (should be the bsky post record)
		require.NotNil(event.Subject)
		if event.Subject.RepoStrongRef != nil {
			// Record-level event
			assert.Equal(postRef.Uri, event.Subject.RepoStrongRef.Uri)
			assert.Equal(postRef.Cid, event.Subject.RepoStrongRef.Cid)
			assert.True(strings.Contains(event.Subject.RepoStrongRef.Uri, "app.flashes.feed.post"), 
				"Expected event subject to be from flashes collection, got: %s", event.Subject.RepoStrongRef.Uri)
		} else if event.Subject.AdminDefs_RepoRef != nil {
			// Account-level event
			assert.Equal(user.DID(), event.Subject.AdminDefs_RepoRef.Did)
		} else {
			t.Fatal("Expected either RepoStrongRef or AdminDefs_RepoRef in event subject")
		}
		
		// Check for CSAM label event
		if event.Event.ModerationDefs_ModEventLabel != nil && 
		   len(event.Event.ModerationDefs_ModEventLabel.CreateLabelVals) > 0 &&
		   event.Event.ModerationDefs_ModEventLabel.CreateLabelVals[0] == "csam" {
			csamLabelFound = true
		}
		
		// Check for external-csam-detection tag event
		if event.Event.ModerationDefs_ModEventTag != nil &&
		   len(event.Event.ModerationDefs_ModEventTag.Add) > 0 &&
		   event.Event.ModerationDefs_ModEventTag.Add[0] == "external-csam-detection" {
			imageTagFound = true
		}

	}
	
	assert.True(csamLabelFound, "Expected CSAM label event from external CSAM service")
	assert.True(imageTagFound, "Expected external-csam-detection tag event from CSAM service")
}

// TestHEPAIntegrationImageMatchDog tests that bsky posts with dog.jpg do NOT trigger CSAM detection
func TestHEPAIntegrationImageMatchDog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HEPA integration test in 'short' test mode")
	}

	assert := assert.New(t)
	
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
	
	// Create mock CSAM server (but don't configure dog.jpg as CSAM)
	csamServer := NewMockCSAMServer(t)
	defer csamServer.Close()
	
	// Setup HEPA with CSAM detection service
	hepa := MustSetupHEPAWithCSAM(t, "ws://"+relay.Host(), didr, csamServer, "test-csam-token")
	// Override collection filter to allow flash posts
	hepa.consumer.(*consumer.FirehoseConsumer).CollectionFilters = []string{"app.flashes."}
	hepa.Run(t)
	defer hepa.Close()
	
	// Create test user and post with dog.jpg image
	user := pds.MustNewUser(t, "testuser.testpds")
	user.PostFlashWithImage(t, "This post contains dog.jpg", "dog.jpg")
	
	// Wait a moment for processing
	time.Sleep(500 * time.Millisecond)
	
	// Verify no ozone events were generated
	events := hepa.GetOzoneEvents()
	assert.Equal(0, len(events), "Expected no ozone events for dog.jpg image, got %d", len(events))
}

// TestHEPAIntegrationNoImage tests that normal flash posts without images do NOT trigger image detection
func TestHEPAIntegrationNoImage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HEPA integration test in 'short' test mode")
	}

	assert := assert.New(t)
	
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
	
	// Create mock CSAM server (not used for this test, but needed for consistency)
	csamServer := NewMockCSAMServer(t)
	defer csamServer.Close()
	
	// Setup HEPA with CSAM detection service
	hepa := MustSetupHEPAWithCSAM(t, "ws://"+relay.Host(), didr, csamServer, "test-csam-token")
	// Override collection filter to allow flash posts
	hepa.consumer.(*consumer.FirehoseConsumer).CollectionFilters = []string{"app.flashes."}
	hepa.Run(t)
	defer hepa.Close()
	
	// Create test user and post normal content without images
	user := pds.MustNewUser(t, "testuser.testpds")
	user.PostFlash(t, "This is a normal flash post without any images")
	
	// Wait a moment for processing
	time.Sleep(500 * time.Millisecond)
	
	// Verify no ozone events were generated
	events := hepa.GetOzoneEvents()
	assert.Equal(0, len(events), "Expected no ozone events for post without images, got %d", len(events))
}

// TestHEPAIntegrationFlashesOnly tests that only flashes posts are processed while bsky posts are filtered out
func TestHEPAIntegrationFlashesOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HEPA integration test in 'short' test mode")
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
	
	// Setup HEPA to subscribe to relay with flashes filtering
	hepa := MustSetupHEPA(t, "ws://"+relay.Host(), didr)
	hepa.Run(t)
	defer hepa.Close()
	
	// Create test users
	bskyUser := pds.MustNewUser(t, "bskyuser.testpds")
	flashUser := pds.MustNewUser(t, "flashuser.testpds")
	
	// Create regular bsky posts (should be filtered out)
	bskyUser.Post(t, "This is a regular bsky post")
	bskyUser.Post(t, gtubeString) // Even GTUBE in bsky should be filtered out
	
	// Create flashes posts (should be processed)
	flashUser.PostFlash(t, "This is a normal flash")
	flashUser.PostFlash(t, gtubeString) // GTUBE in flash should trigger automod
	
	// Wait for processing - only the GTUBE flash should create events
	events := hepa.WaitForOzoneEvents(2, 3*time.Second)
	
	// Should have exactly 2 ozone events (only for the GTUBE flash: label + tag)
	require.Equal(2, len(events), "Expected exactly 2 ozone events (only for GTUBE flash: label + tag)")
	
	// Verify events are for GTUBE detection and from flash collection
	labelFound := false
	tagFound := false
	for _, event := range events {
		// Check that the events are for the flash user, not the bsky user
		if event.Subject != nil && event.Subject.RepoStrongRef != nil {
			// The subject should be from a flashes collection
			assert.True(strings.Contains(event.Subject.RepoStrongRef.Uri, "app.flashes.feed.post"), 
				"Expected event subject to be from flashes collection, got: %s", event.Subject.RepoStrongRef.Uri)
		}
		
		if event.Event.ModerationDefs_ModEventLabel != nil && 
		   len(event.Event.ModerationDefs_ModEventLabel.CreateLabelVals) > 0 &&
		   event.Event.ModerationDefs_ModEventLabel.CreateLabelVals[0] == "spam" {
			labelFound = true
		}
		if event.Event.ModerationDefs_ModEventTag != nil &&
		   len(event.Event.ModerationDefs_ModEventTag.Add) > 0 &&
		   event.Event.ModerationDefs_ModEventTag.Add[0] == "gtube-flash" {
			tagFound = true
		}
	}
	assert.True(labelFound, "Expected spam label event from GTUBE flash")
	assert.True(tagFound, "Expected gtube-flash tag event from GTUBE flash")
}