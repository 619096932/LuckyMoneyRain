#!/usr/bin/env python3
# /// script
# dependencies = ["aiohttp"]
# ///
import argparse
import asyncio
import hashlib
import hmac
import json
import os
import random
import sys
import time
from dataclasses import dataclass
from typing import Dict, List, Optional, Tuple

import aiohttp


def now_ms() -> int:
    return int(time.time() * 1000)


class XorShift32:
    def __init__(self, seed: int):
        self.state = seed & 0xFFFFFFFF

    def next(self) -> int:
        x = self.state
        x ^= (x << 13) & 0xFFFFFFFF
        x ^= (x >> 17) & 0xFFFFFFFF
        x ^= (x << 5) & 0xFFFFFFFF
        self.state = x & 0xFFFFFFFF
        return self.state

    def float(self) -> float:
        return (self.next() & 0xFFFFFFFF) / 4294967295.0


def shuffle(arr: List[int], rng: XorShift32) -> None:
    for i in range(len(arr) - 1, 0, -1):
        j = rng.next() % (i + 1)
        arr[i], arr[j] = arr[j], arr[i]


@dataclass
class Drop:
    drop_id: int
    spawn_at: int
    window_ms: int


def build_slice_drops(slice_cfg: Dict) -> List[Drop]:
    drop_count = int(slice_cfg.get("drop_count", 0))
    bomb_count = int(slice_cfg.get("bomb_count", 0))
    big_count = int(slice_cfg.get("big_count", 0))
    empty_count = int(slice_cfg.get("empty_count", 0))
    seed = int(slice_cfg.get("seed", 0)) & 0xFFFFFFFF
    offsets = slice_cfg.get("offsets_ms")
    big_multiplier = float(slice_cfg.get("big_multiplier", 1.0))
    score_total = int(slice_cfg.get("score_total", 0))
    duration_ms = int(slice_cfg.get("duration_ms", 0))
    window_ms = int(slice_cfg.get("window_ms") or 1200)
    start_at = int(slice_cfg.get("start_at", 0))
    slice_id = int(slice_cfg.get("slice_id", 0))

    if drop_count <= 0:
        return []

    use_offsets = isinstance(offsets, list) and len(offsets) == drop_count
    if not use_offsets:
        rng = XorShift32(seed)
        indices = list(range(drop_count))
        shuffle(indices, rng)
        bomb_set = set(indices[: max(0, bomb_count)])

        non_bomb = indices[max(0, bomb_count) :]
        big_set = set()
        if big_count > 0 and non_bomb:
            shuffle(non_bomb, rng)
            for idx in non_bomb[: min(big_count, len(non_bomb))]:
                big_set.add(idx)

        remain = [idx for idx in non_bomb if idx not in big_set]
        shuffle(remain, rng)
        empty_set = set()
        if empty_count > 0 and remain:
            for idx in remain[: min(empty_count, len(remain))]:
                empty_set.add(idx)

        base_scores = [0 for _ in range(drop_count)]
        scoring = [idx for idx in non_bomb if idx not in empty_set]
        if scoring and score_total > 0:
            total_weight = 0.0
            for idx in scoring:
                total_weight += big_multiplier if idx in big_set else 1.0
            allocated = 0
            for idx in scoring:
                weight = big_multiplier if idx in big_set else 1.0
                val = int((score_total * weight) // total_weight)
                base_scores[idx] = val
                allocated += val
            rem = score_total - allocated
            if rem > 0:
                shuffle(scoring, rng)
                for i in range(rem):
                    base_scores[scoring[i % len(scoring)]] += 1

    drops: List[Drop] = []
    for i in range(drop_count):
        if use_offsets:
            offset = int(offsets[i])
        else:
            max_offset = max(0, duration_ms - window_ms)
            offset = int(rng.float() * (max_offset + 1))
        spawn_at = start_at + offset
        drop_id = slice_id * drop_count + i
        drops.append(Drop(drop_id=drop_id, spawn_at=spawn_at, window_ms=window_ms))
    return drops


@dataclass
class Account:
    phone: str
    nickname: str
    avatar_url: str


@dataclass
class UserSession:
    account: Account
    token: str
    user_id: int


class SimClient:
    def __init__(
        self,
        idx: int,
        session: aiohttp.ClientSession,
        base_url: str,
        ws_url: str,
        token: str,
        user_id: int,
        sign_secret: str,
        click_mode: str,
        burst_sleep_ms: int,
        poll_ms: int,
        click_prob: float,
        reaction_min: int,
        reaction_max: int,
        max_clicks: int,
        verbose: bool,
    ) -> None:
        self.idx = idx
        self.session = session
        self.base_url = base_url
        self.ws_url = ws_url
        self.token = token
        self.user_id = user_id
        self.sign_secret = sign_secret
        self.click_mode = click_mode
        self.burst_sleep_ms = max(0, burst_sleep_ms)
        self.poll_ms = poll_ms
        self.click_prob = max(0.0, min(1.0, click_prob))
        self.reaction_min = max(0, reaction_min)
        self.reaction_max = max(self.reaction_min, reaction_max)
        self.max_clicks = max(0, max_clicks)
        self.verbose = verbose

        self.ws: Optional[aiohttp.ClientWebSocketResponse] = None
        self.poll_task: Optional[asyncio.Task] = None
        self.ws_task: Optional[asyncio.Task] = None
        self.ping_task: Optional[asyncio.Task] = None
        self.click_task: Optional[asyncio.Task] = None

        self.server_offset_ms = 0
        self.round_id = 0
        self.round_status = ""
        self.eligible = False
        self.eligibility_known = False
        self.slices: List[Dict] = []
        self.drop_schedule: List[Drop] = []
        self.schedule_ready = False
        self.result_fetched = False
        self.click_count = 0
        self.ws_connected = False
        self.reconnect_delay_ms = 800
        self.max_reconnect_delay_ms = 8000
        self.sign_key_hex = ""
        self.sign_key_bytes: Optional[bytes] = None
        self.burst_task: Optional[asyncio.Task] = None
        self.burst_drop_ids: List[int] = []
        self.burst_idx = 0

    async def run(self) -> None:
        self.ws_task = asyncio.create_task(self.ws_connect_loop())
        self.poll_task = asyncio.create_task(self.poll_loop())
        await asyncio.gather(self.ws_task, self.poll_task)

    def log(self, msg: str) -> None:
        if self.verbose:
            print(f"[client {self.idx}] {msg}")

    async def connect_ws(self) -> None:
        ws_url = f"{self.ws_url}?token={self.token}"
        self.ws = await self.session.ws_connect(ws_url, heartbeat=20)
        self.ws_connected = True
        self.reconnect_delay_ms = 800
        self.ping_task = asyncio.create_task(self.ping_loop())
        self.log("ws connected")

    async def ws_connect_loop(self) -> None:
        while True:
            try:
                await self.connect_ws()
                await self.ws_loop()
            except Exception:
                pass
            self.ws_connected = False
            if self.ping_task and not self.ping_task.done():
                self.ping_task.cancel()
            if self.ws and not self.ws.closed:
                try:
                    await self.ws.close()
                except Exception:
                    pass
            await asyncio.sleep(self.reconnect_delay_ms / 1000.0)
            self.reconnect_delay_ms = min(self.max_reconnect_delay_ms, int(self.reconnect_delay_ms * 1.5))

    async def ping_loop(self) -> None:
        while True:
            await asyncio.sleep(8)
            if not self.ws or self.ws.closed:
                break
            try:
                await self.ws.send_json({"type": "ping", "ts": now_ms()})
            except Exception:
                break

    async def ws_loop(self) -> None:
        if self.ws is None:
            return
        async for msg in self.ws:
            if msg.type != aiohttp.WSMsgType.TEXT:
                continue
            try:
                data = json.loads(msg.data)
            except Exception:
                continue
            await self.handle_ws_message(data)

    async def handle_ws_message(self, msg: Dict) -> None:
        msg_type = msg.get("type")
        if msg_type == "hello":
            data = msg.get("data", {}) or {}
            self.sync_offset(data.get("server_time"))
            self.update_sign_key(data.get("sign_key"))
            return
        if msg_type == "pong":
            data = msg.get("data", {}) or {}
            self.sync_offset_rtt(data.get("server_time"), data.get("ts"))
            return
        if msg_type == "round_state":
            data = msg.get("data", {})
            self.sync_offset(data.get("server_time"))
            await self.apply_round_state(data)
            return
        if msg_type == "clear_screen":
            self.reset_round(0)
            return
        if msg_type == "round_drawn":
            data = msg.get("data", {})
            await self.fetch_result(data.get("round_id"))

    async def poll_loop(self) -> None:
        if self.poll_ms <= 0:
            return
        while True:
            if not self.ws_connected:
                await self.fetch_game_state()
            await asyncio.sleep(self.poll_ms / 1000.0)

    def sync_offset(self, server_time: Optional[int]) -> None:
        if not server_time:
            return
        new_offset = int(server_time) - now_ms()
        if self.server_offset_ms == 0:
            self.server_offset_ms = new_offset
        else:
            self.server_offset_ms = int(self.server_offset_ms + 0.2 * (new_offset - self.server_offset_ms))

    def sync_offset_rtt(
        self, server_time: Optional[int], client_sent_ts: Optional[int], client_recv_ts: Optional[int] = None
    ) -> None:
        if not server_time or not client_sent_ts:
            return
        sent = int(client_sent_ts)
        recv = int(client_recv_ts) if client_recv_ts is not None else now_ms()
        rtt = recv - sent
        if rtt < 0:
            rtt = 0
        midpoint = sent + (rtt // 2)
        new_offset = int(server_time) - midpoint
        if self.server_offset_ms == 0:
            self.server_offset_ms = new_offset
            return
        if rtt > 5000:
            return
        if rtt <= 80:
            alpha = 0.5
        elif rtt <= 200:
            alpha = 0.3
        elif rtt <= 600:
            alpha = 0.2
        else:
            alpha = 0.1
        self.server_offset_ms = int(self.server_offset_ms + alpha * (new_offset - self.server_offset_ms))

    def update_sign_key(self, key_hex: Optional[str]) -> None:
        if not key_hex:
            return
        if key_hex == self.sign_key_hex:
            return
        try:
            raw = bytes.fromhex(str(key_hex))
        except Exception:
            return
        if not raw:
            return
        self.sign_key_hex = str(key_hex)
        self.sign_key_bytes = raw

    async def api_request(
        self, method: str, path: str, json_body: Optional[Dict] = None, expect_json: bool = True
    ) -> Tuple[int, Dict]:
        headers = {"Authorization": f"Bearer {self.token}"}
        url = self.base_url + path
        try:
            async with self.session.request(method, url, json=json_body, headers=headers) as resp:
                if not expect_json:
                    await resp.read()
                    return resp.status, {}
                text = await resp.text()
                data = json.loads(text) if text else {}
                return resp.status, data
        except Exception:
            return 0, {}

    def should_request_slices(self) -> bool:
        if self.eligibility_known and not self.eligible:
            return False
        if not self.round_status or self.round_status == "WAITING":
            return False
        return not self.slices

    def game_state_path(self, with_slices: bool) -> str:
        return "/api/game/state" if with_slices else "/api/game/state?with_slices=0"

    async def fetch_game_state(self, force_slices: bool = False) -> None:
        with_slices = force_slices or self.should_request_slices()
        t0 = now_ms()
        status, data = await self.api_request("GET", self.game_state_path(with_slices))
        t1 = now_ms()
        if status != 200 or not data:
            return
        self.sync_offset_rtt(data.get("server_time"), t0, t1)
        await self.apply_round_state(data)
        if not with_slices:
            need_slices = (
                self.eligible
                and self.round_status in ("RUNNING", "COUNTDOWN", "LOCKED")
                and not self.slices
            )
            if need_slices:
                await self.fetch_game_state(True)

    async def apply_round_state(self, data: Dict) -> None:
        round_cfg = data.get("round")
        if not round_cfg:
            return
        self.update_sign_key(data.get("sign_key"))
        new_round_id = int(round_cfg.get("id") or 0)
        if new_round_id != self.round_id:
            self.reset_round(new_round_id)

        self.round_status = str(round_cfg.get("status") or "")
        if "eligible" in data:
            self.eligible = bool(data.get("eligible"))
            self.eligibility_known = True

        if "slices" in data and data.get("slices"):
            self.slices = data.get("slices") or []
            if not self.schedule_ready:
                if self.click_mode == "burst":
                    self.build_burst_targets()
                else:
                    self.build_schedule()

        if self.round_status in ("READY_DRAW", "DRAWING", "PENDING_CONFIRM", "FINISHED"):
            await self.fetch_result(self.round_id)

    def reset_round(self, round_id: int) -> None:
        self.round_id = round_id
        self.round_status = ""
        self.eligible = False
        self.slices = []
        self.drop_schedule = []
        self.schedule_ready = False
        self.result_fetched = False
        self.click_count = 0
        if self.click_task and not self.click_task.done():
            self.click_task.cancel()
        self.click_task = None
        if self.burst_task and not self.burst_task.done():
            self.burst_task.cancel()
        self.burst_task = None
        self.burst_drop_ids = []
        self.burst_idx = 0

    def build_schedule(self) -> None:
        drops: List[Drop] = []
        for s in self.slices:
            drops.extend(build_slice_drops(s))
        drops.sort(key=lambda d: d.spawn_at)
        self.drop_schedule = drops
        self.schedule_ready = True
        if self.click_task and not self.click_task.done():
            self.click_task.cancel()
        self.click_task = asyncio.create_task(self.click_loop())
        self.log(f"schedule ready: {len(drops)} drops")

    def build_burst_targets(self) -> None:
        drop_ids: List[int] = []
        for s in self.slices:
            drop_count = int(s.get("drop_count") or 0)
            slice_id = int(s.get("slice_id") or 0)
            if drop_count <= 0:
                continue
            base = slice_id * drop_count
            drop_ids.extend([base + i for i in range(drop_count)])
        self.burst_drop_ids = drop_ids
        self.schedule_ready = True
        if self.burst_task and not self.burst_task.done():
            self.burst_task.cancel()
        self.burst_task = asyncio.create_task(self.burst_loop())
        self.log(f"burst ready: {len(drop_ids)} drops")

    async def click_loop(self) -> None:
        for drop in self.drop_schedule:
            if not self.eligible:
                await asyncio.sleep(0.2)
                continue
            if self.max_clicks and self.click_count >= self.max_clicks:
                break
            if self.click_prob < 1.0 and random.random() > self.click_prob:
                continue
            window = max(50, drop.window_ms)
            max_delay = min(self.reaction_max, max(self.reaction_min, window - 50))
            delay = (
                random.randint(self.reaction_min, max_delay)
                if max_delay >= self.reaction_min
                else int(window * 0.5)
            )
            click_at = drop.spawn_at + delay
            await self.sleep_until_server(click_at)
            await self.send_click(drop)
        self.log("click loop done")

    async def burst_loop(self) -> None:
        if not self.burst_drop_ids:
            return
        while True:
            if not self.eligible or self.round_status != "RUNNING":
                await asyncio.sleep(0.05)
                continue
            if self.max_clicks and self.click_count >= self.max_clicks:
                break
            drop_id = self.burst_drop_ids[self.burst_idx % len(self.burst_drop_ids)]
            self.burst_idx += 1
            await self.send_click(Drop(drop_id=drop_id, spawn_at=0, window_ms=0))
            if self.burst_sleep_ms > 0:
                await asyncio.sleep(self.burst_sleep_ms / 1000.0)
            else:
                await asyncio.sleep(0)
        self.log("burst loop done")

    async def sleep_until_server(self, target_server_ms: int) -> None:
        while True:
            now_server = now_ms() + self.server_offset_ms
            diff = target_server_ms - now_server
            if diff <= 0:
                return
            await asyncio.sleep(min(0.2, diff / 1000.0))

    def make_sign(self, round_id: int, drop_id: int, client_ts: int) -> str:
        key = None
        if self.sign_key_bytes:
            key = self.sign_key_bytes
        elif self.sign_secret:
            key = self.sign_secret.encode()
        else:
            return ""
        msg = f"{self.user_id}|{round_id}|{drop_id}|{client_ts}".encode()
        return hmac.new(key, msg, hashlib.sha256).hexdigest()

    async def send_click(self, drop: Drop) -> None:
        if not self.round_id:
            return
        if self.server_offset_ms:
            client_ts = now_ms() + self.server_offset_ms
        else:
            client_ts = now_ms()
        payload = {
            "round_id": self.round_id,
            "drop_id": drop.drop_id,
            "client_ts": client_ts,
        }
        payload["sign"] = self.make_sign(self.round_id, drop.drop_id, client_ts)
        if not payload["sign"]:
            return
        if self.ws_connected and self.ws and not self.ws.closed:
            try:
                await self.ws.send_str(json.dumps({
                    "type": "c",
                    "data": {
                        "r": payload["round_id"],
                        "d": payload["drop_id"],
                        "t": payload["client_ts"],
                        "s": payload["sign"],
                    },
                }))
            except Exception:
                await self.api_request("POST", "/api/game/click", payload, expect_json=False)
        else:
            await self.api_request("POST", "/api/game/click", payload, expect_json=False)
        self.click_count += 1

    async def fetch_result(self, round_id: Optional[int]) -> None:
        if self.result_fetched or not round_id:
            return
        await self.api_request("GET", f"/api/game/result?round_id={int(round_id)}")
        self.result_fetched = True


def load_accounts(path: str) -> List[Account]:
    if not os.path.exists(path):
        return []
    accounts: List[Account] = []
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            raw = line.strip()
            if not raw or raw.startswith("#"):
                continue
            parts = [p.strip() for p in raw.split(",")]
            phone = parts[0]
            nickname = parts[1] if len(parts) > 1 else f"User{phone[-4:]}"
            avatar = parts[2] if len(parts) > 2 else ""
            accounts.append(Account(phone=phone, nickname=nickname, avatar_url=avatar))
    return accounts


def random_phone(existing: set) -> str:
    while True:
        phone = "1" + "".join(random.choice("0123456789") for _ in range(10))
        if phone not in existing:
            return phone


def ensure_accounts(path: str, accounts: List[Account], need: int) -> None:
    existing = {acc.phone for acc in accounts}
    added: List[Account] = []
    while len(accounts) < need:
        phone = random_phone(existing)
        existing.add(phone)
        nickname = f"User{phone[-4:]}"
        acc = Account(phone=phone, nickname=nickname, avatar_url="")
        accounts.append(acc)
        added.append(acc)
    if not added:
        return
    file_exists = os.path.exists(path)
    mode = "a" if file_exists else "w"
    with open(path, mode, encoding="utf-8") as f:
        if not file_exists:
            f.write("# phone,nickname,avatar_url\n")
        for acc in added:
            f.write(f"{acc.phone},{acc.nickname},{acc.avatar_url}\n")


async def remote_register(
    session: aiohttp.ClientSession,
    base_url: str,
    key: str,
    account: Account,
    retries: int = 3,
) -> Optional[UserSession]:
    url = base_url + "/api/remote/register"
    payload = {
        "key": key,
        "phone": account.phone,
        "nickname": account.nickname,
        "avatar_url": account.avatar_url,
    }
    headers = {"X-Remote-Key": key}
    for i in range(retries):
        try:
            async with session.post(url, json=payload, headers=headers) as resp:
                text = await resp.text()
                data = json.loads(text) if text else {}
                if resp.status == 200 and data.get("token"):
                    user = data.get("user") or {}
                    return UserSession(account=account, token=data["token"], user_id=int(user.get("id") or 0))
        except Exception:
            pass
        await asyncio.sleep(0.2 * (i + 1))
    return None


def build_ws_url(base_url: str) -> str:
    if base_url.startswith("https://"):
        return "wss://" + base_url[len("https://") :] + "/ws"
    if base_url.startswith("http://"):
        return "ws://" + base_url[len("http://") :] + "/ws"
    return "ws://" + base_url + "/ws"


async def main_async(args: argparse.Namespace) -> None:
    base_url = args.base.rstrip("/")
    ws_url = args.ws or build_ws_url(base_url)

    accounts = load_accounts(args.accounts)
    ensure_accounts(args.accounts, accounts, args.concurrency)

    timeout = aiohttp.ClientTimeout(total=30)
    # limit=0 表示无连接数限制 (默认是 100)
    connector = aiohttp.TCPConnector(limit=0, limit_per_host=0)
    async with aiohttp.ClientSession(timeout=timeout, connector=connector) as session:
        sem = asyncio.Semaphore(args.register_batch)

        async def reg_one(acc: Account) -> Optional[UserSession]:
            async with sem:
                return await remote_register(session, base_url, args.remote_key, acc)

        registrations = await asyncio.gather(*[reg_one(acc) for acc in accounts[: args.concurrency]])
        sessions = [s for s in registrations if s]
        if not sessions:
            print("no sessions, aborting")
            return
        if len(sessions) < args.concurrency:
            print(f"registered {len(sessions)} users, expected {args.concurrency}")

        clients = [
            SimClient(
                idx=i + 1,
                session=session,
                base_url=base_url,
                ws_url=ws_url,
                token=s.token,
                user_id=s.user_id,
                sign_secret=args.sign_secret,
                click_mode=args.click_mode,
                burst_sleep_ms=args.burst_sleep_ms,
                poll_ms=args.poll_ms,
                click_prob=args.click_prob,
                reaction_min=args.reaction_min,
                reaction_max=args.reaction_max,
                max_clicks=args.max_clicks,
                verbose=args.verbose,
            )
            for i, s in enumerate(sessions)
        ]
        tasks = [asyncio.create_task(client.run()) for client in clients]
        await asyncio.gather(*tasks)


def parse_args(argv: List[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Simple WS + click simulator (index.html flow).")
    parser.add_argument("--base", default="****", help="Base URL")
    parser.add_argument("--ws", default="", help="WS URL, default from base")
    parser.add_argument("--accounts", default="accounts.txt", help="Account list file")
    parser.add_argument("--concurrency", type=int, default=0, help="Simulated users")
    parser.add_argument("--remote-key", default="*****", help="Remote register key")
    parser.add_argument("--sign-secret", default="", help="Game sign secret if enabled")
    parser.add_argument(
        "--click-mode",
        default="schedule",
        choices=["schedule", "burst"],
        help="Click mode: schedule (follow drops) or burst (max QPS)",
    )
    parser.add_argument("--burst-sleep-ms", type=int, default=0, help="Sleep between burst clicks (ms)")
    parser.add_argument("--poll-ms", type=int, default=2000, help="Poll interval (ms)")
    parser.add_argument("--click-prob", type=float, default=1.0, help="Click probability per drop")
    parser.add_argument("--reaction-min", type=int, default=80, help="Min reaction delay (ms)")
    parser.add_argument("--reaction-max", type=int, default=500, help="Max reaction delay (ms)")
    parser.add_argument("--max-clicks", type=int, default=0, help="Max clicks per user, 0=unlimited")
    parser.add_argument("--register-batch", type=int, default=20, help="Concurrent register requests")
    parser.add_argument("--verbose", action="store_true", help="Verbose logs")
    return parser.parse_args(argv)


def main() -> None:
    args = parse_args(sys.argv[1:])
    if args.concurrency <= 0:
        try:
            raw = input("Concurrency (e.g. 100): ").strip()
            args.concurrency = int(raw)
        except Exception:
            print("invalid concurrency")
            return
    if args.concurrency <= 0:
        print("concurrency must be > 0")
        return
    asyncio.run(main_async(args))


if __name__ == "__main__":
    main()
