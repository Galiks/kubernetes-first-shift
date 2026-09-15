"""Unit-тесты классификации HTTP-статусов worker."""

import pytest

from relayforge.worker.classifier import classify


@pytest.mark.parametrize(
    "status,expected",
    [
        (200, "success"),
        (201, "success"),
        (204, "success"),
        (408, "transient"),
        (429, "transient"),
        (500, "transient"),
        (502, "transient"),
        (503, "transient"),
        (400, "permanent"),
        (401, "permanent"),
        (404, "permanent"),
        (422, "permanent"),
        (300, "permanent"),  # redirect — ошибка
        (301, "permanent"),
        (302, "permanent"),
        (0, "transient"),
    ],
)
def test_classify(status, expected):
    assert classify(status) == expected