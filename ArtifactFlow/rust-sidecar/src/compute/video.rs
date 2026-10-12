use std::fs;
use std::path::Path;
use std::process::Command;

pub fn extract_frames(run_id: &str, node_id: &str, video_uri: &str, fps: f32) -> Result<Vec<String>, String> {
    println!("Extracting frames from {} at {} FPS", video_uri, fps);
    
    // Create output directory for this run/node
    let out_dir = format!("./output/{}_{}", run_id, node_id);
    if let Err(e) = fs::create_dir_all(&out_dir) {
        return Err(format!("Failed to create output directory: {}", e));
    }

    // FFmpeg command to extract frames
    // ffmpeg -i <video_uri> -vf fps=<fps> <out_dir>/frame_%04d.jpg
    let out_pattern = format!("{}/frame_%04d.jpg", out_dir);
    
    let status = Command::new("ffmpeg")
        .args(&[
            "-y", // overwrite
            "-i", video_uri,
            "-vf", &format!("fps={}", fps),
            &out_pattern
        ])
        .status();

    match status {
        Ok(s) if s.success() => {
            println!("FFmpeg extraction successful.");
        }
        Ok(s) => {
            return Err(format!("FFmpeg failed with exit status: {}", s));
        }
        Err(e) => {
            return Err(format!("Failed to execute FFmpeg (is it installed?): {}", e));
        }
    }

    // Read the output directory to get the generated frame URIs
    let mut extracted_uris = Vec::new();
    let paths = fs::read_dir(&out_dir).map_err(|e| e.to_string())?;

    for path in paths {
        if let Ok(entry) = path {
            let path_str = entry.path().to_string_lossy().to_string();
            // In Windows, replace backslashes to standard forward slashes for cross-platform URI consistency
            let normalized_path = path_str.replace("\\", "/");
            extracted_uris.push(normalized_path);
        }
    }

    // Sort to maintain chronological order
    extracted_uris.sort();

    Ok(extracted_uris)
}
