"""
Compliance Rules and Risk Scoring Engine for Bytfin Identity Worker.
Adheres to RBZ SI 99 of 2026 (Virtual Asset Service Providers) and FATF Travel Rule.
"""

from typing import Dict, Any, Tuple

TIER_LIMITS = {
    0: {"name": "Unverified", "daily_limit_usd": 0.0, "monthly_limit_usd": 0.0},
    1: {"name": "Basic (ID+Phone)", "daily_limit_usd": 200.0, "monthly_limit_usd": 1000.0},
    2: {"name": "Full (OCR+Biometrics)", "daily_limit_usd": 2000.0, "monthly_limit_usd": 10000.0},
    3: {"name": "Merchant/Enterprise", "daily_limit_usd": float('inf'), "monthly_limit_usd": float('inf')}
}

def evaluate_transaction_risk(amount_usd: float, tier: int, tx_history_last_hour: int = 1) -> Tuple[bool, str, Dict[str, Any]]:
    """
    Evaluates whether a transaction is compliant with RBZ velocity limits and flags suspicious patterns.
    """
    tier_info = TIER_LIMITS.get(tier, TIER_LIMITS[0])
    
    # 1. Check Daily Limit
    if amount_usd > tier_info["daily_limit_usd"]:
        return False, f"Transaction amount ${amount_usd:.2f} exceeds Tier {tier} daily limit of ${tier_info['daily_limit_usd']:.2f}", {
            "status": "REJECTED_LIMIT_EXCEEDED",
            "tier": tier,
            "max_allowed": tier_info["daily_limit_usd"]
        }
    
    # 2. Velocity Anomaly / Structuring Check
    is_suspicious = False
    flag_reason = "NONE"
    
    if tx_history_last_hour > 15 and tier < 3:
        is_suspicious = True
        flag_reason = "HIGH_FREQUENCY_VELOCITY_ANOMALY"
    elif 190.0 <= amount_usd < 200.0 and tier == 1:
        is_suspicious = True
        flag_reason = "POTENTIAL_STRUCTURING_NEAR_TIER_LIMIT"

    # 3. Travel Rule Requirement Flag
    requires_travel_rule = amount_usd >= 1000.0

    return True, "Approved", {
        "status": "APPROVED",
        "requires_travel_rule": requires_travel_rule,
        "is_suspicious_for_str": is_suspicious,
        "flag_reason": flag_reason,
        "tier": tier
    }
