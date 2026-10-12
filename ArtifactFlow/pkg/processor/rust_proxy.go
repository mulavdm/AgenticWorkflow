package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ArtifactFlow/ArtifactFlow/pkg/artifact"
	"github.com/ArtifactFlow/ArtifactFlow/pkg/ipc"
	"github.com/user/CentralLogger"
	flatbuffers "github.com/google/flatbuffers/go"

	// Generated FlatBuffers package
	"github.com/ArtifactFlow/ArtifactFlow/pkg/ipc/artifactflow"
)

// RustProxyProcessor forwards execution to the Rust sidecar via IPC.
type RustProxyProcessor struct {
	processorName string
	ipcClient     *ipc.Client
}

func NewRustProxyProcessor(name string, client *ipc.Client) *RustProxyProcessor {
	return &RustProxyProcessor{
		processorName: name,
		ipcClient:     client,
	}
}

func (p *RustProxyProcessor) Name() string {
	return p.processorName
}

func (p *RustProxyProcessor) Process(ctx context.Context, config map[string]any, inputs []artifact.Artifact) ([]artifact.Artifact, error) {
	// Serialize config to JSON string
	configBytes, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize config: %w", err)
	}
	configStr := string(configBytes)

	// Since we don't have access to run_id and node_id here in the interface natively (for MVP),
	// we will generate dummy ones or extract them if they are passed in config (ideal architecture refactor later).
	runID := "run"
	nodeID := "node"

	builder := flatbuffers.NewBuilder(1024)

	// Serialize Inputs
	var inputOffsets []flatbuffers.UOffsetT
	for _, in := range inputs {
		idOffset := builder.CreateString(in.ID)
		kindOffset := builder.CreateString(string(in.Kind))
		uriOffset := builder.CreateString(in.URI)

		artifactflow.ArtifactSchemaStart(builder)
		artifactflow.ArtifactSchemaAddId(builder, idOffset)
		artifactflow.ArtifactSchemaAddKind(builder, kindOffset)
		artifactflow.ArtifactSchemaAddUri(builder, uriOffset)
		inputOffsets = append(inputOffsets, artifactflow.ArtifactSchemaEnd(builder))
	}

	artifactflow.NodeExecutionRequestStartInputsVector(builder, len(inputOffsets))
	// Flatbuffers requires building vectors backwards
	for i := len(inputOffsets) - 1; i >= 0; i-- {
		builder.PrependUOffsetT(inputOffsets[i])
	}
	inputsVec := builder.EndVector(len(inputOffsets))

	// Create Request
	runIdOff := builder.CreateString(runID)
	nodeIdOff := builder.CreateString(nodeID)
	procNameOff := builder.CreateString(p.processorName)
	cfgJsonOff := builder.CreateString(configStr)

	artifactflow.NodeExecutionRequestStart(builder)
	artifactflow.NodeExecutionRequestAddRunId(builder, runIdOff)
	artifactflow.NodeExecutionRequestAddNodeId(builder, nodeIdOff)
	artifactflow.NodeExecutionRequestAddProcessorName(builder, procNameOff)
	artifactflow.NodeExecutionRequestAddConfigJson(builder, cfgJsonOff)
	artifactflow.NodeExecutionRequestAddInputs(builder, inputsVec)
	reqOffset := artifactflow.NodeExecutionRequestEnd(builder)

	// Wrap in IPCMessage
	msgId := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	msgIdOff := builder.CreateString(msgId)

	artifactflow.IPCMessageStart(builder)
	artifactflow.IPCMessageAddId(builder, msgIdOff)
	artifactflow.IPCMessageAddPayloadType(builder, artifactflow.IPCMessagePayloadNodeExecutionRequest)
	artifactflow.IPCMessageAddPayload(builder, reqOffset)
	msgOffset := artifactflow.IPCMessageEnd(builder)

	builder.Finish(msgOffset)
	buf := builder.FinishedBytes()

	logger.Info("RustProxy", fmt.Sprintf("Sending %s request to Rust sidecar...", p.processorName))
	respBuf, err := p.ipcClient.SendMessage(buf)
	if err != nil {
		return nil, fmt.Errorf("ipc request failed: %w", err)
	}

	// Deserialize Response
	respMsg := artifactflow.GetRootAsIPCMessage(respBuf, 0)
	if respMsg.PayloadType() != artifactflow.IPCMessagePayloadNodeExecutionResponse {
		return nil, fmt.Errorf("unexpected response payload type: %d", respMsg.PayloadType())
	}

	var respTable flatbuffers.Table
	if !respMsg.Payload(&respTable) {
		return nil, fmt.Errorf("failed to extract payload table")
	}

	resp := new(artifactflow.NodeExecutionResponse)
	resp.Init(respTable.Bytes, respTable.Pos)

	if !resp.Success() {
		return nil, fmt.Errorf("rust sidecar execution failed: %s", string(resp.ErrorMessage()))
	}

	// Parse Output Artifacts
	var outputs []artifact.Artifact
	for i := 0; i < resp.OutputsLength(); i++ {
		var artSchema artifactflow.ArtifactSchema
		if resp.Outputs(&artSchema, i) {
			outputs = append(outputs, artifact.Artifact{
				ID:   string(artSchema.Id()),
				Kind: artifact.Kind(artSchema.Kind()),
				URI:  string(artSchema.Uri()),
			})
		}
	}

	logger.Info("RustProxy", fmt.Sprintf("Received %d artifacts from Rust sidecar.", len(outputs)))
	return outputs, nil
}
