class ModeController:
    def __init__(self):
        self.mode = "normal"
        self.fail_first_n = 0
        self.attempts: dict[str, int] = {}

    def set_mode(self, mode: str, n: int = 1) -> None:
        self.mode = mode
        self.fail_first_n = n
        self.attempts.clear()

    def reset(self) -> None:
        self.mode = "normal"
        self.fail_first_n = 0
        self.attempts.clear()

    def check_mode(self, delivery_id: str) -> str:
        self.attempts[delivery_id] = self.attempts.get(delivery_id, 0) + 1
        n = self.attempts[delivery_id]

        if self.mode == "fail-first":
            if n <= self.fail_first_n:
                return "fail"
            return "accept"
        if self.mode == "accept-and-drop":
            return "drop"
        if self.mode == "reject":
            return "reject"
        if self.mode == "slow":
            return "slow"
        return "accept"