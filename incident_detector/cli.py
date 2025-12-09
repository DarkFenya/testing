"""Command-line interface for incident detection across conversations."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Dict, List

from .detector import ConversationMatches, IncidentDetector
from .triggers import ProblemType, build_problem_types


def _write_match_file(
    match: ConversationMatches, output_dir: Path, problem_types: Dict[str, ProblemType]
) -> None:
    conversation_dir = output_dir / "conversations"
    conversation_dir.mkdir(parents=True, exist_ok=True)

    def serialize_messages(messages: List) -> List[dict]:
        return [
            {
                "index": message.index,
                "user_id": message.user_id,
                "text": message.text,
                "triggers": message.triggers,
            }
            for message in messages
        ]

    payload = {
        "dialog_id": match.dialog_id,
        "source_path": str(match.source_path),
        "matched_types": [
            {
                "type": type_key,
                "type_name": problem_types[type_key].name,
                "triggers": sorted({t for msg in messages for t in msg.triggers}),
                "messages": serialize_messages(messages),
            }
            for type_key, messages in sorted(match.matched_types.items())
        ],
        "unique_triggers": match.flat_triggers,
    }

    with open(conversation_dir / f"{match.dialog_id}.json", "w", encoding="utf-8") as fp:
        json.dump(payload, fp, ensure_ascii=False, indent=2)


def _write_index(matches: List[ConversationMatches], output_dir: Path, problem_types: Dict[str, ProblemType]) -> None:
    stats: Dict[str, List[str]] = {key: [] for key in problem_types}
    for match in matches:
        for type_key in match.matched_types:
            stats[type_key].append(match.dialog_id)

    lines = ["# Индекс проблемных диалогов", ""]
    lines.append("## Количество по типам")
    lines.append("")

    for type_key, problem_type in problem_types.items():
        dialogs = sorted(stats[type_key])
        lines.append(f"### {problem_type.name}")
        lines.append(f"- Найдено диалогов: {len(dialogs)}")
        if dialogs:
            sample = ", ".join(dialogs[:10])
            lines.append(f"- Примеры: {sample}")
        lines.append("")

    lines.append("## Все сохраненные диалоги")
    lines.append("")
    for match in sorted(matches, key=lambda m: m.dialog_id):
        types = ", ".join(
            problem_types[type_key].name for type_key in sorted(match.matched_types)
        )
        lines.append(f"- {match.dialog_id}: {types}")

    output_dir.mkdir(parents=True, exist_ok=True)
    (output_dir / "INDEX.md").write_text("\n".join(lines), encoding="utf-8")


def _print_summary(matches: List[ConversationMatches], detector: IncidentDetector) -> None:
    print("Найдено проблемных диалогов:", len(matches))
    per_type: Dict[str, int] = {key: 0 for key in detector.problem_types}
    for match in matches:
        for type_key in match.matched_types:
            per_type[type_key] += 1

    for type_key, count in per_type.items():
        name = detector.problem_types[type_key].name
        print(f"- {name}: {count}")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Поиск инцидентных запросов, требующих перевода на оператора",
    )
    parser.add_argument(
        "--input",
        default="output/conversations",
        help="Папка с диалогами (по умолчанию output/conversations)",
    )
    parser.add_argument(
        "--output",
        default="incident_reports",
        help="Папка для сохранения результатов (по умолчанию incident_reports)",
    )
    return parser


def main() -> None:
    parser = build_parser()
    args = parser.parse_args()

    input_dir = Path(args.input)
    if not input_dir.exists():
        parser.error(f"Папка с диалогами не найдена: {input_dir}")

    detector = IncidentDetector(build_problem_types())
    matches, _ = detector.scan(str(input_dir))

    output_dir = Path(args.output)
    for match in matches:
        _write_match_file(match, output_dir, detector.problem_types)

    _write_index(matches, output_dir, detector.problem_types)
    _print_summary(matches, detector)


if __name__ == "__main__":
    main()
