import gc
import torch
from typing import Any, Callable
from collections import OrderedDict
from .hardware import get_best_device, is_dml, get_free_vram

class HardwareCompatibilityError(Exception):
    pass

class ModelCache:
    """Smart LRU Cache to manage VRAM with proactive and reactive eviction."""
    def __init__(self):
        self.device = get_best_device()
        self.is_dml_env = is_dml()
        self.models = OrderedDict()
        
    def _evict_oldest(self):
        """Pops the oldest model from the cache and clears VRAM."""
        if not self.models:
            return False
            
        repo, _ = self.models.popitem(last=False)
        print(f"[ModelCache] Evicting model to free VRAM: {repo}")
        
        gc.collect()
        if not self.is_dml_env and torch.cuda.is_available():
            torch.cuda.empty_cache()
            
        return True

    def get_or_load(self, repo: str, loader_func: Callable[[], Any], min_vram_bytes: int = 0) -> Any:
        # If already loaded, move to end (mark as most recently used)
        if repo in self.models:
            model = self.models.pop(repo)
            self.models[repo] = model
            return model
            
        print(f"[ModelCache] Loading model: {repo}")
        
        # Proactive Eviction: Check if we have enough VRAM before loading
        free_vram = get_free_vram()
        while free_vram is not None and min_vram_bytes > 0 and free_vram < min_vram_bytes:
            if not self._evict_oldest():
                break # Cache is empty, can't evict more
            free_vram = get_free_vram()
        
        # Reactive Eviction: Load and catch OOMs
        while True:
            try:
                model = loader_func()
                self.models[repo] = model
                return model
            except RuntimeError as e:
                # Catch OOM errors gracefully
                if "out of memory" in str(e).lower() or "allocate" in str(e).lower():
                    print(f"[ModelCache] Caught OOM trying to load {repo}. Attempting reactive eviction...")
                    if self._evict_oldest():
                        continue # Try again
                
                # If we couldn't evict, or it wasn't an OOM, crash out
                self._evict_oldest() # Clean up any partial state
                raise HardwareCompatibilityError(f"OOM or load error: {str(e)}")
