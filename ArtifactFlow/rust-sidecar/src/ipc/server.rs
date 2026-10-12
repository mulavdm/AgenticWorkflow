use crate::ipc::workflow_generated::artifactflow::{
    ArtifactSchema, ArtifactSchemaArgs, IPCMessage, IPCMessagePayload, NodeExecutionResponse,
    NodeExecutionResponseArgs,
};
use flatbuffers::FlatBufferBuilder;
use interprocess::local_socket::{ListenerOptions, Stream, prelude::*};
use std::io::{self, Read, Write};

pub fn start_server(pipe_name: &str) -> io::Result<()> {
    let name_result = interprocess::local_socket::ToNsName::to_ns_name::<
        interprocess::local_socket::GenericNamespaced,
    >(pipe_name);
    let name = match name_result {
        Ok(n) => n,
        Err(e) => {
            eprintln!("Failed to parse pipe name: {}", e);
            return Err(e);
        }
    };

    let opts = ListenerOptions::new().name(name);
    let listener = match opts.create_sync() {
        Ok(l) => l,
        Err(e) => {
            if e.kind() == io::ErrorKind::AddrInUse {
                eprintln!("Error: Socket already in use. Stale socket file/pipe?");
            }
            return Err(e);
        }
    };

    println!("Rust IPC Server listening on: {}", pipe_name);

    for conn in listener.incoming().filter_map(handle_error) {
        handle_connection(conn)?;
    }

    Ok(())
}

fn handle_error(conn: io::Result<Stream>) -> Option<Stream> {
    match conn {
        Ok(c) => Some(c),
        Err(e) => {
            eprintln!("Incoming connection failed: {}", e);
            None
        }
    }
}

fn handle_connection(mut conn: Stream) -> io::Result<()> {
    println!("Go Orchestrator connected to IPC bridge.");
    let mut len_buf = [0u8; 4];

    loop {
        if let Err(e) = conn.read_exact(&mut len_buf) {
            if e.kind() == io::ErrorKind::UnexpectedEof {
                println!("Client disconnected.");
                break;
            }
            return Err(e);
        }

        let msg_len = u32::from_le_bytes(len_buf) as usize;
        let mut msg_buf = vec![0u8; msg_len];
        conn.read_exact(&mut msg_buf)?;

        let response_buf = process_message(&msg_buf);

        let resp_len = response_buf.len() as u32;
        conn.write_all(&resp_len.to_le_bytes())?;
        conn.write_all(&response_buf)?;
    }

    Ok(())
}

fn process_message(buf: &[u8]) -> Vec<u8> {
    let mut builder = FlatBufferBuilder::new();

    let msg = match flatbuffers::root::<IPCMessage>(buf) {
        Ok(m) => m,
        Err(e) => {
            eprintln!("Failed to parse IPC message: {}", e);
            return vec![];
        }
    };

    println!("Received IPC Message ID: {:?}", msg.id());

    match msg.payload_type() {
        IPCMessagePayload::NodeExecutionRequest => {
            if let Some(req) = msg.payload_as_node_execution_request() {
                let proc_name = req.processor_name().unwrap_or("");
                println!("Routing request for processor: {}", proc_name);

                let mut success = false;
                let mut error_msg = String::new();
                let mut output_artifacts = Vec::new();

                if proc_name == "video.extract_frames" {
                    // Extract inputs (expecting a video URI)
                    let inputs = req.inputs().unwrap();
                    if inputs.len() > 0 {
                        let video_uri = inputs.get(0).uri().unwrap_or("");
                        let run_id = req.run_id().unwrap_or("run");
                        let node_id = req.node_id().unwrap_or("node");

                        // TODO: Read config JSON to get FPS, for now default to 1.0
                        let fps = 1.0;

                        match crate::compute::video::extract_frames(run_id, node_id, video_uri, fps) {
                            Ok(uris) => {
                                success = true;
                                for uri in uris {
                                    output_artifacts.push((
                                        "extracted_frame".to_string(),
                                        "image/jpeg".to_string(),
                                        uri,
                                    ));
                                }
                            }
                            Err(e) => {
                                success = false;
                                error_msg = e;
                            }
                        }
                    } else {
                        success = false;
                        error_msg = "No input artifacts provided".to_string();
                    }
                } else {
                    success = false;
                    error_msg = format!("Unknown processor: {}", proc_name);
                }

                // Build response
                let mut artifact_offsets = Vec::new();
                for art in output_artifacts {
                    let id_offset = builder.create_string(&art.0);
                    let kind_offset = builder.create_string(&art.1);
                    let uri_offset = builder.create_string(&art.2);
                    
                    let art_args = ArtifactSchemaArgs {
                        id: Some(id_offset),
                        kind: Some(kind_offset),
                        uri: Some(uri_offset),
                    };
                    artifact_offsets.push(ArtifactSchema::create(&mut builder, &art_args));
                }

                let outputs_vec = builder.create_vector(&artifact_offsets);
                let error_msg_offset = builder.create_string(&error_msg);

                let resp_args = NodeExecutionResponseArgs {
                    success,
                    error_message: Some(error_msg_offset),
                    outputs: Some(outputs_vec),
                };
                let resp_offset = NodeExecutionResponse::create(&mut builder, &resp_args);

                let id_offset = builder.create_string(msg.id().unwrap_or(""));
                let msg_args = crate::ipc::workflow_generated::artifactflow::IPCMessageArgs {
                    id: Some(id_offset),
                    payload_type: IPCMessagePayload::NodeExecutionResponse,
                    payload: Some(resp_offset.as_union_value()),
                };

                let final_msg = IPCMessage::create(&mut builder, &msg_args);
                builder.finish(final_msg, None);
            }
        }
        _ => {
            eprintln!("Unknown payload type");
        }
    }

    builder.finished_data().to_vec()
}
