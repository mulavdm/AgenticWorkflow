import gc
import torch

# MONKEY PATCH: Fix PyTorch < 2.4 missing FP8 attribute bug for transformers 4.41+
if not hasattr(torch, "float8_e8m0fnu"):
    torch.float8_e8m0fnu = None

from transformers import BlipProcessor, BlipForConditionalGeneration
from transformers import BlipProcessor, BlipForConditionalGeneration
from artifactflow_sdk import ModelCache, HardwareCompatibilityError

# Create global agnostic cache
cache = ModelCache()

class BLIPWrapper:
    def __init__(self, repo: str):
        self.processor = BlipProcessor.from_pretrained(repo)
        self.model = BlipForConditionalGeneration.from_pretrained(repo, use_safetensors=True).to(cache.device)

def _load_blip(repo: str):
    print(f"[ModelManager] Loading BLIP: {repo}")
    return BLIPWrapper(repo)

def get_model(repo: str):
    # Pass lambda to the generic get_or_load SDK function
    wrapper = cache.get_or_load(repo, lambda: _load_blip(repo))
    return wrapper.model, wrapper.processor

# Global instance

def generate_caption(repo: str, prompt: str, image_path: str) -> str:
    from PIL import Image
    model, processor = get_model(repo)
    
    try:
        raw_image = Image.open(image_path).convert('RGB')
        
        # MVP: Only BLIP
        inputs = processor(raw_image, prompt, return_tensors="pt").to(cache.device)
        out = model.generate(**inputs, max_new_tokens=200)
        return processor.decode(out[0], skip_special_tokens=True)

    except RuntimeError as e:
        # If execution fails (e.g. missing DirectML op), flush VRAM and bubble up
        cache._evict_oldest()
        raise HardwareCompatibilityError(f"Runtime error during generation: {str(e)}")
