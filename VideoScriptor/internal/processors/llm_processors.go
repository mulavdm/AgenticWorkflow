package processors

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/models"
)

type MockVideoProcessor struct{}

func (m *MockVideoProcessor) Name() string { return "tool.mock_video" }
func (m *MockVideoProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	fmt.Println("[MockVideo] Creating dummy video...")

	outPath := filepath.Join("output", "dummy_video.mp4")
	os.MkdirAll("output", 0755)

	// Create a real 1-second blank mp4 with a 1kHz audio track so downstream audio/video extraction works
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "color=c=black:s=640x480:d=1", "-f", "lavfi", "-i", "sine=frequency=1000:duration=1", "-c:v", "libx264", "-c:a", "aac", outPath)
	cmd.Run()

	return []artifact.Artifact{{
		ID:   "dummy_video",
		Kind: "video",
		URI:  outPath,
	}}, nil
}

type TranscribeProcessor struct {
	Models *models.Registry
}

func NewTranscribeProcessor(r *models.Registry) *TranscribeProcessor {
	return &TranscribeProcessor{Models: r}
}
func (p *TranscribeProcessor) Name() string { return "audio.whisper" }
func (p *TranscribeProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	fmt.Println("[WhisperProcessor] Requesting transcription capability...")

	// Normally we would parse config for model ID. Hardcoding for MVP.
	modelID := "whisper_model"
	if mID, ok := config["model"].(string); ok {
		modelID = mID
	}

	videoPath := filepath.Join("output", "dummy_video.mp4")
	audioPath := filepath.Join("output", "dummy_audio.wav")
	os.MkdirAll("output", 0755)

	// Extract 16kHz WAV for Whisper
	exec.Command("ffmpeg", "-y", "-i", videoPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", audioPath).Run()
	audioAbs, _ := filepath.Abs(audioPath)

	req := models.GenerateRequest{
		ModelID: modelID,
		Prompt:  "Transcribe the following audio.",
		Audio:   audioAbs,
	}

	res, err := p.Models.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("model execution failed: %w", err)
	}
	fmt.Printf("[WhisperProcessor] Result: %s\n", res.Text)

	outPath := filepath.Join("output", "transcript.json")

	transcript := []map[string]any{
		{"Start": 0, "End": 1, "Text": res.Text},
	}
	b, _ := json.Marshal(transcript)
	os.WriteFile(outPath, b, 0644)

	return []artifact.Artifact{{
		ID:   "transcript",
		Kind: "transcript",
		URI:  outPath,
	}}, nil
}

type VisionCaptionProcessor struct {
	Models *models.Registry
}

func NewVisionCaptionProcessor(r *models.Registry) *VisionCaptionProcessor {
	return &VisionCaptionProcessor{Models: r}
}
func (p *VisionCaptionProcessor) Name() string { return "vision.describe" }
func (p *VisionCaptionProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	fmt.Println("[VisionProcessor] Requesting image.describe capability...")

	modelID := "vision_model"
	if mID, ok := config["model"].(string); ok {
		modelID = mID
	}

	absPath, _ := filepath.Abs(filepath.Join("output", "run_node", "frame_0001.jpg"))

	req := models.GenerateRequest{
		ModelID: modelID,
		Prompt:  "Describe this frame in detail.",
		Images:  []string{absPath},
	}

	res, err := p.Models.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("model execution failed: %w", err)
	}
	fmt.Printf("[VisionProcessor] Result: %s\n", res.Text)

	outPath := filepath.Join("output", "visuals.jsonl")
	os.MkdirAll("output", 0755)

	visual := map[string]any{
		"Start": 0, "End": 10,
		"Description": res.Text,
		"Actions":     []string{},
		"Setting":     "",
	}
	b, _ := json.Marshal(visual)
	os.WriteFile(outPath, append(b, '\n'), 0644)

	return []artifact.Artifact{{
		ID:   "visuals",
		Kind: "visual_descriptions",
		URI:  outPath,
	}}, nil
}

type ScriptMergeProcessor struct {
	Models *models.Registry
}

func NewScriptMergeProcessor(r *models.Registry) *ScriptMergeProcessor {
	return &ScriptMergeProcessor{Models: r}
}
func (p *ScriptMergeProcessor) Name() string { return "text.merge_script" }
func (p *ScriptMergeProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	fmt.Println("[ScriptMergeProcessor] Requesting text.generate capability...")

	modelID := "text_model"
	if mID, ok := config["model"].(string); ok {
		modelID = mID
	}

	transcriptBytes, _ := os.ReadFile(filepath.Join("output", "transcript.json"))
	visualsBytes, _ := os.ReadFile(filepath.Join("output", "visuals.jsonl"))

	req := models.GenerateRequest{
		ModelID: modelID,
		Prompt:  fmt.Sprintf("Merge the transcript and visual description into a screenplay.\n\nTranscript: %s\n\nVisuals: %s", string(transcriptBytes), string(visualsBytes)),
	}

	res, err := p.Models.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("model execution failed: %w", err)
	}
	fmt.Printf("[ScriptMergeProcessor] Result: %s\n", res.Text)

	outPath := filepath.Join("output", "script.md")
	os.MkdirAll("output", 0755)

	os.WriteFile(outPath, []byte(res.Text), 0644)

	return []artifact.Artifact{{
		ID:   "final_script",
		Kind: "markdown",
		URI:  outPath,
	}}, nil
}
