"""Глобальное состояние test-sink (одно на Pod).

Отдельный модуль — чтобы избежать circular import между test_sink.app
и test_sink.routes. Счётчики попыток и факт применения хранятся в
ReceiptStore (файлы на emptyDir) и переживают restart контейнера.
"""


class SinkState:
    def __init__(self):
        self.verification_key: bytes = b""
        self.control_token: bytes = b""
        self.modes = None  # test_sink.modes.ModeController
        self.receipts = None  # test_sink.receipts.ReceiptStore


state = SinkState()