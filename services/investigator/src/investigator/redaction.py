import re
import json

_bearer = re.compile(r"(?i)bearer\s+[A-Za-z0-9._~+/=-]+")
_assignment = re.compile(r"(?i)(password|api[_-]?key|token|secret|authorization)\s*[:=]\s*[^\s,;]+")
_url = re.compile(r"(?i)(https?://)[^\s/:@]+:[^\s/@]+@")
_private = re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----", re.S)

def sanitize(value):
    if isinstance(value, dict):
        return {key: "[REDACTED]" if (any(term in key.lower().replace("_","").replace("-","") for term in ("password","secret","credential","authorization","apikey")) or key.lower().replace("_","").replace("-","") in ("token","accesstoken","refreshtoken","idtoken","sessiontoken")) else sanitize(item) for key,item in value.items()}
    if isinstance(value, list):
        return [sanitize(item) for item in value]
    if isinstance(value, str):
        try:
            encoded=json.loads(value)
            if isinstance(encoded,(dict,list)):return json.dumps(sanitize(encoded))
        except (ValueError,TypeError):pass
        value=_url.sub(r"\1[REDACTED]@",value)
        return _assignment.sub("[REDACTED]", _bearer.sub("Bearer [REDACTED]", _private.sub("[REDACTED]",value)))
    return value
