from abc import ABC, abstractmethod
from typing import Type
from pydantic import BaseModel

class Processor(ABC):
    @property
    @abstractmethod
    def name(self) -> str:
        """The unique identifier for this processor, matching the Go DAG node processor ID."""
        pass
        
    @property
    @abstractmethod
    def config_model(self) -> Type[BaseModel]:
        """The Pydantic BaseModel to validate the incoming configuration."""
        pass

    @abstractmethod
    def process(self, config: BaseModel) -> dict:
        """Execute the processor logic. Returns a dictionary (e.g., {'tensor_path': '...'})"""
        pass
