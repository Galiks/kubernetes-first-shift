import pytest
from relayforge.canonical import strict_json_loads, canonicalize


def test_sorted_keys():
    assert canonicalize({"b": 1, "a": 2}) == b'{"a":2,"b":1}'


def test_int_vs_float():
    assert canonicalize({"x": 1}) != canonicalize({"x": 1.0})


def test_duplicate_keys_rejected():
    with pytest.raises(ValueError):
        strict_json_loads('{"a":1,"a":2}')


def test_nan_rejected():
    with pytest.raises(ValueError):
        strict_json_loads('{"x": NaN}')


def test_infinity_rejected():
    with pytest.raises(ValueError):
        strict_json_loads('{"x": Infinity}')