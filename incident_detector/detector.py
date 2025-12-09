"""Conversation scanning utilities."""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from typing import Dict, Iterable, List, Optional

from .triggers import CONV_FILE_PATTERN, ProblemType, build_problem_types


@dataclass
class MatchedMessage:
    index: int
    user_id: str
    text: str
    triggers: List[str]


@dataclass
class ConversationMatches:
    dialog_id: str
    source_path: str
    matched_types: Dict[str, List[MatchedMessage]] = field(default_factory=dict)

    def add_match(self, type_key: str, message: MatchedMessage) -> None:
        self.matched_types.setdefault(type_key, []).append(message)

    @property
    def flat_triggers(self) -> List[str]:
        result = []
        for messages in self.matched_types.values():
            for message in messages:
                for trigger in message.triggers:
                    if trigger not in result:
                        result.append(trigger)
        return result


@dataclass
class ScanStats:
    total_conversations: int = 0
    matched_conversations: int = 0
    per_type: Dict[str, int] = field(default_factory=dict)

    def register_conversation(self, matched_types: Iterable[str]) -> None:
        self.total_conversations += 1
        matched = list(matched_types)
        if matched:
            self.matched_conversations += 1
        for type_key in matched:
            self.per_type[type_key] = self.per_type.get(type_key, 0) + 1


class IncidentDetector:
    def __init__(self, problem_types: Optional[Dict[str, ProblemType]] = None) -> None:
        self.problem_types = problem_types or build_problem_types()

    def _extract_dialog_id(self, filename: str, fallback: str) -> str:
        match = CONV_FILE_PATTERN.search(filename)
        if match:
            return match.group(1)
        return fallback

    def _iter_conversation_files(self, input_dir: str) -> Iterable[tuple[str, str]]:
        for entry in os.scandir(input_dir):
            if entry.is_dir():
                for nested in os.scandir(entry.path):
                    if nested.is_file() and nested.name.endswith("_chat.json"):
                        yield nested.path, entry.name
            elif entry.is_file() and entry.name.endswith("_chat.json"):
                yield entry.path, os.path.splitext(entry.name)[0]

    def _find_pattern_matches(self, text: str, type_key: str) -> List[str]:
        matches: List[str] = []
        type_info = self.problem_types.get(type_key)
        if not type_info:
            return matches
        for pattern in type_info.patterns:
            if pattern.search(text):
                matches.extend(pattern.findall(text))
        return matches

    def analyze_messages(self, messages: List[dict]) -> Dict[str, List[MatchedMessage]]:
        matched: Dict[str, List[MatchedMessage]] = {}
        for idx, message in enumerate(messages):
            user_id = message.get("user_id", "")
            if not user_id.startswith("user_"):
                continue
            text = message.get("text", "")
            for type_key in self.problem_types:
                matches = self._find_pattern_matches(text, type_key)
                if not matches:
                    continue
                unique_matches = sorted({m.lower() for m in matches})
                matched_message = MatchedMessage(
                    index=idx,
                    user_id=user_id,
                    text=text,
                    triggers=unique_matches,
                )
                matched.setdefault(type_key, []).append(matched_message)
        return matched

    def analyze_conversation(self, file_path: str, folder_name: str) -> Optional[ConversationMatches]:
        try:
            with open(file_path, "r", encoding="utf-8") as fp:
                payload = json.load(fp)
        except (OSError, json.JSONDecodeError) as exc:
            print(f"Не удалось прочитать {file_path}: {exc}")
            return None

        messages = payload.get("messages", [])
        dialog_id = self._extract_dialog_id(os.path.basename(file_path), folder_name)
        matched_types = self.analyze_messages(messages)

        if not matched_types:
            return None

        result = ConversationMatches(dialog_id=dialog_id, source_path=file_path)
        for type_key, messages in matched_types.items():
            for message in messages:
                result.add_match(type_key, message)
        return result

    def scan(self, input_dir: str) -> tuple[List[ConversationMatches], ScanStats]:
        matches: List[ConversationMatches] = []
        stats = ScanStats()
        for file_path, folder_name in self._iter_conversation_files(input_dir):
            analysis = self.analyze_conversation(file_path, folder_name)
            if analysis:
                matches.append(analysis)
                stats.register_conversation(analysis.matched_types.keys())
            else:
                stats.register_conversation([])
        return matches, stats


def scan_conversations(input_dir: str) -> tuple[List[ConversationMatches], ScanStats]:
    detector = IncidentDetector()
    return detector.scan(input_dir)

