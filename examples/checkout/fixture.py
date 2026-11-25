#!/usr/bin/env python3
"""Deterministic checkout & saga fixture server + lifecycle hooks for agent-fault-check."""

import http.server
import json
import os
import sqlite3
import sys
import urllib.parse

DB_PATH = os.environ.get("AFC_FIXTURE_DB", "/tmp/afc_checkout_fixture.db")
PG_DSN = os.environ.get("AFC_TEST_POSTGRES_DSN", "")


def _connect_pg(dsn):
    try:
        import psycopg
        return psycopg.connect(dsn)
    except ImportError:
        try:
            import psycopg2
            return psycopg2.connect(dsn)
        except ImportError as exc:
            raise RuntimeError(
                "AFC_TEST_POSTGRES_DSN is set, but neither psycopg nor psycopg2 is installed. "
                "Install psycopg[binary] or unset AFC_TEST_POSTGRES_DSN to use SQLite."
            ) from exc


class DB:
    """Thin dual-backend wrapper supporting Postgres (when AFC_TEST_POSTGRES_DSN is set) or SQLite."""

    def __init__(self):
        self.is_pg = bool(PG_DSN)
        if self.is_pg:
            self.conn = _connect_pg(PG_DSN)
            self._init_schema_pg()
        else:
            self.conn = sqlite3.connect(DB_PATH, timeout=10.0)
            self.conn.execute("PRAGMA journal_mode=WAL;")
            self._init_schema_sqlite()

    def _init_schema_sqlite(self):
        self.conn.execute(
            """CREATE TABLE IF NOT EXISTS orders (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id TEXT NOT NULL,
                customer_id TEXT NOT NULL,
                amount_cents INTEGER NOT NULL,
                idempotency_key TEXT
            )"""
        )
        self.conn.execute(
            """CREATE TABLE IF NOT EXISTS saga_events (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id TEXT NOT NULL,
                step TEXT NOT NULL
            )"""
        )
        self.conn.commit()

    def _init_schema_pg(self):
        with self.conn.cursor() as cur:
            cur.execute(
                """CREATE TABLE IF NOT EXISTS orders (
                    id SERIAL PRIMARY KEY,
                    run_id TEXT NOT NULL,
                    customer_id TEXT NOT NULL,
                    amount_cents INTEGER NOT NULL,
                    idempotency_key TEXT
                )"""
            )
            cur.execute(
                """CREATE TABLE IF NOT EXISTS saga_events (
                    id SERIAL PRIMARY KEY,
                    run_id TEXT NOT NULL,
                    step TEXT NOT NULL
                )"""
            )
        self.conn.commit()

    def _q(self, sql):
        return sql.replace("?", "%s") if self.is_pg else sql

    def execute(self, sql, params=()):
        cur = self.conn.cursor()
        cur.execute(self._q(sql), params)
        return cur

    def insert_order(self, run_id, customer_id, amount_cents, idem):
        if self.is_pg:
            cur = self.execute(
                "INSERT INTO orders (run_id, customer_id, amount_cents, idempotency_key) VALUES (?, ?, ?, ?) RETURNING id",
                (run_id, customer_id, amount_cents, idem),
            )
            row_id = cur.fetchone()[0]
            cur.close()
            self.conn.commit()
            return row_id
        cur = self.execute(
            "INSERT INTO orders (run_id, customer_id, amount_cents, idempotency_key) VALUES (?, ?, ?, ?)",
            (run_id, customer_id, amount_cents, idem),
        )
        self.conn.commit()
        return cur.lastrowid

    def commit(self):
        self.conn.commit()

    def close(self):
        self.conn.close()


def get_conn():
    return DB()


def cmd_setup():
    run_id = os.environ.get("AFC_RUN_ID", "default")
    conn = get_conn()
    conn.execute("DELETE FROM orders WHERE run_id = ?", (run_id,))
    conn.execute("DELETE FROM saga_events WHERE run_id = ?", (run_id,))
    conn.commit()
    conn.close()


def cmd_quiesce():
    # SQLite writes in this fixture are synchronous; verify DB is readable.
    conn = get_conn()
    conn.execute("SELECT 1").fetchone()
    conn.close()


def cmd_teardown():
    run_id = os.environ.get("AFC_RUN_ID", "default")
    conn = get_conn()
    conn.execute("DELETE FROM orders WHERE run_id = ?", (run_id,))
    conn.execute("DELETE FROM saga_events WHERE run_id = ?", (run_id,))
    conn.commit()
    conn.close()


class FixtureHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        return

    def _read_json(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length) if length > 0 else b"{}"
        return json.loads(raw.decode("utf-8"))

    def _send_json(self, status, payload):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        parsed = urllib.parse.urlparse(self.path)
        data = self._read_json()
        run_id = data.get("run_id") or self.headers.get("X-Run-ID", "default")
        conn = get_conn()
        try:
            if parsed.path == "/api/v1/orders":
                idem = self.headers.get("X-Idempotency-Key")
                if idem:
                    existing = conn.execute(
                        "SELECT id FROM orders WHERE run_id = ? AND idempotency_key = ?",
                        (run_id, idem),
                    ).fetchone()
                    if existing:
                        self._send_json(201, {"id": existing[0], "deduped": True})
                        return
                order_id = conn.insert_order(
                    run_id,
                    data.get("customer_id", "cust_123"),
                    int(data.get("amount_cents", 4900)),
                    idem,
                )
                self._send_json(201, {"id": order_id, "deduped": False})
                return

            if parsed.path == "/api/v1/inventory/reserve":
                conn.execute("INSERT INTO saga_events (run_id, step) VALUES (?, 'reserved')", (run_id,))
                conn.commit()
                self._send_json(201, {"status": "reserved"})
                return

            if parsed.path == "/api/v1/inventory/release":
                conn.execute("DELETE FROM saga_events WHERE run_id = ? AND step = 'reserved'", (run_id,))
                conn.execute("INSERT INTO saga_events (run_id, step) VALUES (?, 'released')", (run_id,))
                conn.commit()
                self._send_json(200, {"status": "released"})
                return

            if parsed.path == "/api/v1/payments/charge":
                conn.execute("INSERT INTO saga_events (run_id, step) VALUES (?, 'charged')", (run_id,))
                conn.commit()
                self._send_json(201, {"status": "charged"})
                return

            self._send_json(404, {"error": "not found"})
        finally:
            conn.close()

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        qs = urllib.parse.parse_qs(parsed.query)
        run_id = qs.get("run_id", ["default"])[0]
        conn = get_conn()
        try:
            if parsed.path == "/state/orders":
                row = conn.execute("SELECT COUNT(*) FROM orders WHERE run_id = ?", (run_id,)).fetchone()
                self._send_json(200, int(row[0]))
                return
            if parsed.path == "/state/reserved":
                row = conn.execute(
                    "SELECT COUNT(*) FROM saga_events WHERE run_id = ? AND step = 'reserved'", (run_id,)
                ).fetchone()
                self._send_json(200, int(row[0]))
                return
            if parsed.path == "/state/released":
                row = conn.execute(
                    "SELECT COUNT(*) FROM saga_events WHERE run_id = ? AND step = 'released'", (run_id,)
                ).fetchone()
                self._send_json(200, int(row[0]))
                return
            self._send_json(404, {"error": "not found"})
        finally:
            conn.close()


def cmd_server():
    host = os.environ.get("AFC_FIXTURE_HOST", "127.0.0.1")
    port = int(os.environ.get("AFC_FIXTURE_PORT", "8000"))
    server = http.server.ThreadingHTTPServer((host, port), FixtureHandler)
    print(f"LISTENING {host}:{server.server_address[1]}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(2)
    cmd = sys.argv[1]
    if cmd == "setup":
        cmd_setup()
    elif cmd == "quiesce":
        cmd_quiesce()
    elif cmd == "teardown":
        cmd_teardown()
    elif cmd == "server":
        cmd_server()
    else:
        sys.exit(2)
