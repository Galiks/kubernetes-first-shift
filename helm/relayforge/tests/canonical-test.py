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


def test_float_overflow_rejected():
    # 1e999 стандартным parse_float превратился бы в float('inf'),
    # а orjson сериализует его как null — молча ломая canonical hash.
    with pytest.raises(ValueError):
        strict_json_loads('{"x": 1e999}')


def test_negative_float_overflow_rejected():
    # -1e999 → float('-inf') — тоже не finite.
    with pytest.raises(ValueError):
        strict_json_loads('{"x": -1e999}')


def test_normal_float_accepted():
    assert strict_json_loads('{"x": 1.5}') == {"x": 1.5}


def test_canonical_int_vs_float_overflow_not_corrupted():
    # Переполнившийся литерал не должен доходить до canonicalize как null.
    with pytest.raises(ValueError):
        strict_json_loads('{"x": 1e999}')