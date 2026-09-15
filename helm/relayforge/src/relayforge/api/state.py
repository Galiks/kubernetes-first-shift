"""Глобальное состояние API-процесса (одно на Pod).

Отдельный модуль, чтобы избежать circular import между api.app и api.routes.
"""


class AppState:
    def __init__(self):
        self.ready: bool = False
        self.destinations: dict = {}
        self.k8s = None  # kubernetes_asyncio BatchV1Api
        self.core = None  # kubernetes_asyncio CoreV1Api
        self.job_registry = None  # relayforge.api.job_registry.JobRegistry
        self.backpressure = None  # relayforge.api.backpressure.Backpressure
        self.client_token: bytes = b""


state = AppState()