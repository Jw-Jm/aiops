"""Bounded exception structure without exception messages, URLs or headers."""
import traceback


def failure_frames(error):
    pending = [error]
    seen = set()
    frames = []
    while pending and len(seen) < 16:
        current = pending.pop()
        if id(current) in seen:
            continue
        seen.add(id(current))
        name = type(current).__name__
        status = getattr(getattr(current, "response", None), "status_code", None)
        frames.append((name, status if isinstance(status, int) else None))
        for frame in traceback.extract_tb(current.__traceback__)[-8:]:
            frames.append((name, frame.filename, frame.lineno))
        if isinstance(current, BaseExceptionGroup):
            pending.extend(reversed(current.exceptions))
        elif current.__cause__ or current.__context__:
            pending.append(current.__cause__ or current.__context__)
    return frames
