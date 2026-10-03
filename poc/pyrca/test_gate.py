from adapter import evaluate_holdout

def test_disabled_without_all_formal_admission():
    result=evaluate_holdout()
    assert result["runtimeEnabled"] is False
    assert result["falseConfirmedIncrease"] == 0
    assert result["cases"] == 9
    assert result["resourceLatencyAdmission"] == "unverified-user-performance-waiver"
