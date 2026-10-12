from fastapi import FastAPI, HTTPException
import uvicorn
from typing import Type
from pydantic import BaseModel
from .processor import Processor

class SidecarServer:
    def __init__(self, title: str = "ArtifactFlow Sidecar"):
        self.app = FastAPI(title=title)
        
        @self.app.get("/health")
        def health_check():
            return {"status": "ready"}

    def register(self, processor: Processor):
        """Registers a processor to the FastAPI app dynamically."""
        
        # We create a wrapper endpoint for the processor.
        # FastAPI requires the exact Pydantic model type in the function signature to auto-validate.
        ModelType = processor.config_model
        
        # We use a closure to bind the specific model type and processor
        def endpoint_wrapper(req: ModelType):
            try:
                return processor.process(req)
            except Exception as e:
                raise HTTPException(status_code=500, detail=str(e))
                
        # Register the endpoint with FastAPI. We use the processor's name as the path.
        # Example: name "gen.encode_prompt" -> POST /gen.encode_prompt
        # Note: We must ensure the endpoint path starts with a slash
        path = f"/{processor.name}"
        if not path.startswith("/"):
            path = "/" + path
            
        self.app.post(path)(endpoint_wrapper)
        print(f"Registered processor: {processor.name} at {path}")

    def run(self, host: str = "127.0.0.1", port: int = 8000):
        uvicorn.run(self.app, host=host, port=port)
