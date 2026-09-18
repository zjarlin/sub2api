from dataclasses import dataclass, field
from typing import Optional


@dataclass(frozen=True)
class OutputFormat:
    kind: str = 'text'
    schema: Optional[dict] = None
    description: str = ''


@dataclass(frozen=True)
class ToolPolicy:
    functions: dict = field(default_factory=dict)
    choice: str = 'none'
    forced_name: Optional[str] = None
    parallel: bool = True

    @property
    def enabled(self):
        return bool(self.functions) and self.choice != 'none'


@dataclass(frozen=True)
class ChatRequest:
    model: str
    text: str
    stream: bool
    output_format: OutputFormat
    tools: ToolPolicy
    messages: list = field(default_factory=list)


@dataclass(frozen=True)
class ConversationCursor:
    conversation_id: str
    section_id: str
    message_index: int
