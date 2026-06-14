"""Stage adapters.

``mapper.py`` and ``fakes.py`` are PURE (contracts + interfaces only, no
optional deps) and form the testable core. ``*_real.py`` / ``ocr_paddle`` /
``classifier_haiku`` / ``inferencer_opus`` / ``pii_presidio`` are the ONLY
modules allowed to lazy-import optional extras, and nothing in the core may
import them at module load (the config factory resolves them by string).
"""
