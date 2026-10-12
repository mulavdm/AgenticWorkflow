import os
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from typing import List, Optional
from model_manager import generate_caption, HardwareCompatibilityError

app = FastAPI(title="ArtifactFlow Python Sidecar")

class GenerateRequest(BaseModel):
    model_repo: str
    prompt: str
    image_paths: Optional[List[str]] = None

class GenerateResponse(BaseModel):
    text: str

@app.get("/health")
def health_check():
    return {"status": "ready"}

@app.post("/generate", response_model=GenerateResponse)
def generate(req: GenerateRequest):
    # Only supporting single image vision capability right now
    if not req.image_paths or len(req.image_paths) == 0:
        raise HTTPException(status_code=400, detail="Vision generation requires at least one image_path")
        
    image_path = req.image_paths[0]
    if not os.path.exists(image_path):
        raise HTTPException(status_code=404, detail=f"Image file not found: {image_path}")

    try:
        text = generate_caption(req.model_repo, req.prompt, image_path)
        return GenerateResponse(text=text)
    except HardwareCompatibilityError as e:
        raise HTTPException(status_code=501, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Generation failed: {str(e)}")

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="127.0.0.1", port=8000)
