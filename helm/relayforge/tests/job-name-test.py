from relayforge.api.job_builder import job_name


def test_deterministic():
    n1 = job_name("relay-a", "order-service:invoice-1842:v1")
    n2 = job_name("relay-a", "order-service:invoice-1842:v1")
    assert n1 == n2


def test_max_length():
    n = job_name("relay-a", "x" * 128)
    assert len(n) <= 63


def test_different_releases():
    n1 = job_name("relay-a", "key")
    n2 = job_name("relay-b", "key")
    assert n1 != n2