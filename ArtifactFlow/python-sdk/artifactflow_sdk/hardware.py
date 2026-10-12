import torch
import warnings
from typing import Optional

def is_dml() -> bool:
    """Returns True if the current accelerator is AMD DirectML."""
    try:
        import torch_directml
        return torch_directml.is_available()
    except ImportError:
        return False

def get_best_device():
    """Returns the most capable hardware device available."""
    if torch.cuda.is_available():
        print("Hardware Auto-Discovery: Binding to CUDA (Nvidia GPU)")
        return "cuda"
    
    # Check for DirectML (Windows AMD)
    if is_dml():
        import torch_directml
        print("Hardware Auto-Discovery: Binding to DirectML (AMD/Windows GPU)")
        return torch_directml.device()
        
    warnings.warn("No hardware acceleration found. Falling back to CPU. This will be very slow!")
    print("Hardware Auto-Discovery: Binding to CPU")
    return "cpu"

def get_free_vram() -> Optional[int]:
    """
    Returns the free VRAM in bytes if the device supports querying (e.g., CUDA).
    Returns None for devices where querying is unsupported (DirectML/CPU).
    """
    if torch.cuda.is_available() and not is_dml():
        try:
            free, _ = torch.cuda.mem_get_info()
            return free
        except Exception:
            return None
    return None
