package worker

import (
	"context"
	"fmt"
	"log"
	"time"

	"talkforge-be/config"
	"talkforge-be/model"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// Worker polls pending dialogue generation requests and processes them asynchronously.
type Worker struct {
	cfg *config.Config
}

// NewWorker instantiates a new Worker.
func NewWorker(cfg *config.Config) *Worker {
	return &Worker{cfg: cfg}
}

// Start launches the worker loop with a polling mechanism.
func (w *Worker) Start(ctx context.Context) {
	log.Println("Starting TalkForge background queue worker...")
	ticker := time.NewTicker(time.Duration(w.cfg.QueuePollIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping queue worker...")
			return
		case <-ticker.C:
			w.processNextRequest(ctx)
		}
	}
}

func (w *Worker) processNextRequest(ctx context.Context) {
	var reqs []model.TalkRequest

	// Find the oldest pending request
	err := model.DB.WithContext(ctx).
		Where("status = ?", "pending").
		Order("created_at asc").
		Limit(1).
		Find(&reqs).Error

	if err != nil {
		log.Printf("Worker error querying pending requests: %v", err)
		return
	}

	if len(reqs) == 0 {
		return // No pending requests
	}

	req := reqs[0]

	// Update status to processing
	req.Status = "processing"
	req.UpdatedAt = time.Now()
	if err := model.DB.Save(&req).Error; err != nil {
		log.Printf("Worker error updating status to processing for request %d: %v", req.ID, err)
		return
	}

	log.Printf("Worker: Processing request %d (Mode: %s, User: %d)...", req.ID, req.Mode, req.UserID)

	// Execute processing
	err = w.executeRequest(ctx, &req)
	if err != nil {
		log.Printf("Worker error executing request %d: %v", req.ID, err)

		// Update status to failed
		req.Status = "failed"
		req.ErrorMessage = err.Error()
		req.UpdatedAt = time.Now()
		_ = model.DB.Save(&req)
	} else {
		log.Printf("Worker: Successfully completed request %d", req.ID)
	}
}

func (w *Worker) executeRequest(ctx context.Context, req *model.TalkRequest) error {
	// Initialize Gemini Client
	if w.cfg.GeminiAPIKey == "" {
		return fmt.Errorf("GEMINI_API_KEY is not configured in environment")
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(w.cfg.GeminiAPIKey))
	if err != nil {
		return fmt.Errorf("failed to init Gemini client: %v", err)
	}
	defer client.Close()

	// Use gemini-3.1-flash-lite as requested
	geminiModel := client.GenerativeModel("gemini-3.1-flash-lite")

	var systemInstruction string
	var promptMessage string

	wordCount := req.Duration * 130 // A standard speaking rate is about 130 words per minute

	if req.Mode == "new" {
		systemInstruction = "You are an expert speechwriter. Your goal is to write a highly polished, natural, and engaging speech based on the specified speaker role (such as a politician, an imam, or a teacher). Adapt the tone, rhetoric, vocabulary, and structural style to suit the requested role and the delivery location. Output ONLY the raw speech text itself, without any introductory or concluding meta-commentary, notes, or markdown formatting (no backticks or ```)."
		promptMessage = fmt.Sprintf(
			"Please write a speech in %s for a speaker acting as a '%s', to be delivered at '%s'. The speech must take approximately %d minutes to read aloud at a standard speaking rate (approx. %d words). The topic and details of the speech are:\n%s",
			req.Language,
			req.SpeechType,
			req.Place,
			req.Duration,
			wordCount,
			req.Topic,
		)
	} else if req.Mode == "update" {
		if req.ParentID == nil {
			return fmt.Errorf("parent_id is required for update mode")
		}

		var parent model.TalkRequest
		if err := model.DB.First(&parent, *req.ParentID).Error; err != nil {
			return fmt.Errorf("parent request %d not found in database: %v", *req.ParentID, err)
		}

		if parent.Status != "completed" {
			return fmt.Errorf("parent request %d has not completed successfully", *req.ParentID)
		}

		systemInstruction = "You are an expert speechwriter editing an existing speech. Rewrite the provided speech strictly conforming to the new user instructions, maintaining the speaker role and location context. Output ONLY the raw modified speech text itself, without any introductory or concluding meta-commentary, notes, or markdown formatting (no backticks or ```)."
		promptMessage = fmt.Sprintf(
			"ORIGINAL SPEECH:\n%s\n\nUPDATE/REWRITE INSTRUCTION:\n%s\n\nPlease edit and rewrite the speech above based on the instruction. Ensure the revised speech still takes approximately %d minutes to read aloud (approx. %d words).",
			parent.GeneratedText,
			req.Instruction,
			req.Duration,
			wordCount,
		)
	} else {
		return fmt.Errorf("unknown mode: %s", req.Mode)
	}

	geminiModel.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemInstruction)},
	}

	// Call Gemini API
	resp, err := geminiModel.GenerateContent(ctx, genai.Text(promptMessage))
	if err != nil {
		return fmt.Errorf("gemini API error: %v", err)
	}

	// Extract response text
	var responseText string
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if txt, ok := part.(genai.Text); ok {
				responseText += string(txt)
			}
		}
	}

	if responseText == "" {
		return fmt.Errorf("empty response received from Gemini model")
	}

	// Save results
	req.GeneratedText = responseText
	req.Status = "completed"
	req.UpdatedAt = time.Now()

	return model.DB.Save(req).Error
}
