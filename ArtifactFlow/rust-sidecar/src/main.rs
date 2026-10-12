mod compute;
mod ipc;

fn main() {
    println!("Rust Sidecar (Compute Engine) starting...");

    let pipe_name = "artifactflow_ipc";

    if let Err(e) = ipc::server::start_server(pipe_name) {
        eprintln!("Fatal error in IPC server: {}", e);
        std::process::exit(1);
    }
}
