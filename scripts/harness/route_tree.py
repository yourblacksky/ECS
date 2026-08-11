#!/usr/bin/env python3
"""Deterministic recursive routing for ECS context hints."""

from __future__ import annotations

import fnmatch
import json
import pathlib
import re
from dataclasses import dataclass
from typing import Any


MAX_NODES = 128
MAX_DEPTH = 16


@dataclass(frozen=True)
class Node:
    id: str
    parent: str | None
    rank: int
    patterns: tuple[str, ...]
    contexts: tuple[str, ...]


@dataclass(frozen=True)
class Match:
    id: str
    ancestry: tuple[str, ...]
    contexts: tuple[str, ...]


def parse(raw: bytes | str | dict[str, Any]) -> tuple[str, dict[str, Node], dict[str, int]]:
    value = json.loads(raw) if isinstance(raw, (bytes, str)) else raw
    if not isinstance(value, dict) or value.get("version") != 2:
        raise ValueError("unsupported ECS Harness route tree")
    root = value.get("root")
    rows = value.get("nodes")
    budgets = value.get("budgets")
    if not isinstance(root, str) or not isinstance(rows, list) or not isinstance(budgets, dict):
        raise ValueError("invalid ECS Harness route tree")
    if not 0 < len(rows) <= MAX_NODES:
        raise ValueError(f"route tree must contain 1..{MAX_NODES} nodes")
    parsed_budgets = {str(key): int(limit) for key, limit in budgets.items()}
    required_budgets = {
        "startup_total_bytes",
        "agents_max_bytes",
        "session_context_max_bytes",
        "runtime_hint_max_bytes",
    }
    if set(parsed_budgets) != required_budgets or any(limit <= 0 for limit in parsed_budgets.values()):
        raise ValueError("invalid ECS Harness context budgets")
    nodes: dict[str, Node] = {}
    for row in rows:
        if not isinstance(row, dict):
            raise ValueError("route tree node must be an object")
        node_id = row.get("id")
        parent = row.get("parent")
        rank = row.get("rank")
        patterns = row.get("patterns")
        contexts = row.get("contexts")
        if (
            not isinstance(node_id, str)
            or (parent is not None and not isinstance(parent, str))
            or not isinstance(rank, int)
            or isinstance(rank, bool)
        ):
            raise ValueError("invalid route node field types")
        if not isinstance(patterns, list) or not all(
            isinstance(item, str) and item for item in patterns
        ):
            raise ValueError(f"route node patterns must be non-empty strings: {node_id}")
        if not isinstance(contexts, list) or not all(
            isinstance(item, str) and item for item in contexts
        ):
            raise ValueError(f"route node contexts must be non-empty strings: {node_id}")
        node = Node(
            id=node_id,
            parent=parent,
            rank=rank,
            patterns=tuple(patterns),
            contexts=tuple(contexts),
        )
        if node.id in nodes:
            raise ValueError(f"duplicate route node: {node.id}")
        nodes[node.id] = node
    validate(root, nodes)
    return root, nodes, parsed_budgets


def validate(root: str, nodes: dict[str, Node]) -> None:
    if root not in nodes or nodes[root].parent is not None:
        raise ValueError("route root is missing or has a parent")
    sibling_ranks: set[tuple[str | None, int]] = set()
    for node in nodes.values():
        if not re.fullmatch(r"[a-z][a-z0-9-]*", node.id):
            raise ValueError(f"invalid route node id: {node.id}")
        if node.rank < 0:
            raise ValueError(f"invalid route node metadata: {node.id}")
        if node.id != root and (node.parent not in nodes or not node.patterns):
            raise ValueError(f"route node has no reachable parent/pattern: {node.id}")
        rank_key = (node.parent, node.rank)
        if rank_key in sibling_ranks:
            raise ValueError(f"ambiguous sibling route rank under: {node.parent}")
        sibling_ranks.add(rank_key)
        for value in (*node.patterns, *node.contexts):
            path = value.split("#", 1)[0]
            if path.startswith("/") or ".." in pathlib.PurePosixPath(path).parts:
                raise ValueError(f"unsafe route value in {node.id}: {value}")
        seen: set[str] = set()
        current: Node | None = node
        depth = 0
        while current is not None:
            if current.id in seen:
                raise ValueError(f"route tree cycle at: {current.id}")
            seen.add(current.id)
            depth += 1
            if depth > MAX_DEPTH:
                raise ValueError(f"route branch exceeds depth {MAX_DEPTH}")
            current = nodes.get(current.parent) if current.parent else None
    reachable = {node.id for node in walk(root, nodes)}
    if reachable != set(nodes):
        raise ValueError("route tree contains unreachable nodes")


def children(parent: str, nodes: dict[str, Node]) -> tuple[Node, ...]:
    return tuple(
        sorted(
            (node for node in nodes.values() if node.parent == parent),
            key=lambda node: (node.rank, node.id),
        )
    )


def walk(node_id: str, nodes: dict[str, Node]) -> tuple[Node, ...]:
    output = [nodes[node_id]]
    for child in children(node_id, nodes):
        output.extend(walk(child.id, nodes))
    return tuple(output)


def lineage(node_id: str, nodes: dict[str, Node]) -> tuple[str, ...]:
    output: list[str] = []
    current: Node | None = nodes[node_id]
    while current is not None:
        output.append(current.id)
        current = nodes.get(current.parent) if current.parent else None
    return tuple(reversed(output))


def matches_path(node: Node, path: str) -> bool:
    return any(fnmatch.fnmatch(path, pattern) for pattern in node.patterns)


def active_for_path(root: str, nodes: dict[str, Node], path: str) -> set[str]:
    active = {root}

    def descend(parent: str) -> None:
        for child in children(parent, nodes):
            if matches_path(child, path):
                active.add(child.id)
                descend(child.id)

    descend(root)
    return active


def select(raw: bytes | str | dict[str, Any], paths: tuple[str, ...]) -> tuple[Match, ...]:
    root, nodes, _ = parse(raw)
    active = {root}
    for path in paths:
        active.update(active_for_path(root, nodes, path))
    return tuple(
        Match(node.id, lineage(node.id, nodes), node.contexts)
        for node in walk(root, nodes)
        if node.id in active
    )
