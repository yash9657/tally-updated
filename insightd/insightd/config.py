"""Runtime settings + the stage factory.

The factory is the single place that decides, per stage, whether to instantiate
a deterministic FAKE (the default — no key, no network, no heavy dep) or a REAL
adapter (only when the matching ``USE_REAL_*`` flag is set). Real adapters are
imported HERE by string at call time, so nothing in the testable core imports
anthropic/paddleocr/presidio at module load.
"""

from __future__ import annotations

import importlib
import os
from typing import TYPE_CHECKING

from pydantic_settings import BaseSettings, SettingsConfigDict

if TYPE_CHECKING:  # pragma: no cover - typing only
    from .interfaces.base import (
        Classifier,
        Ingest,
        Inferencer,
        OCRExtractor,
        PIIRedactor,
        VoucherDraftMapper,
    )

# Pinned model ids — the wire contract for the cross-border calls. Never read
# from a mutable place; changing a model id is a deliberate code change.
CLASSIFIER_MODEL = "claude-haiku-4-5"
INFERENCER_MODEL = "claude-opus-4-8"

# The GST ruleset insightd's mapper re-derives against. MUST track cored's.
RULESET_VERSION = "gst-2024-09"


class Settings(BaseSettings):
    """Process configuration.

    Defaults select the deterministic fakes so tests/CI run with zero keys and
    zero optional deps. Setting any ``use_real_*`` flag opts that ONE stage into
    its real adapter.
    """

    model_config = SettingsConfigDict(env_prefix="INSIGHTD_", extra="ignore")

    # Stage selection — default fakes.
    use_real_ingest: bool = False
    use_real_ocr: bool = False
    use_real_classifier: bool = False
    use_real_inferencer: bool = False
    use_real_pii: bool = False

    # cored relationship is READ-ONLY (health/ruleset version only). insightd
    # holds NO write client and constructs NO /vouchers URL.
    cored_base_url: str = "http://localhost:8080"

    # The cross-border PII boundary is fail-closed by default. Disabling it is
    # rejected at startup unless an explicit DEV override env is also set.
    pii_boundary_strict: bool = True

    # THE SERVICE CREDENTIAL (env INSIGHTD_SERVICE_TOKEN, register row D49).
    # Shared by exactly two processes — cored's draft proxy and this one — and
    # never by a browser. Unset/empty means UNCONFIGURED, and an unconfigured
    # insightd REFUSES every company-scoped request with a 503 rather than
    # serving it open; see api/authn.py for what it is checked against and why
    # that is not the shared secret api/app.py refuses.
    service_token: str | None = None

    # Draft-store selection (env INSIGHTD_DATABASE_URL). Unset/empty -> the
    # in-memory DraftStore (the default; drafts die with the process). Set to a
    # Postgres DSN -> the RLS-scoped PGDraftStore, so drafts survive a restart.
    # psycopg is an OPTIONAL extra (requirements-db.txt), lazy-imported only
    # when this is set — the testable core keeps zero db deps.
    database_url: str | None = None

    # Pinned model ids surfaced on Settings for adapters to read.
    classifier_model: str = CLASSIFIER_MODEL
    inferencer_model: str = INFERENCER_MODEL
    ruleset_version: str = RULESET_VERSION

    def validate_boundary(self) -> None:
        """Fail-closed: refuse to start with the PII boundary disabled unless an
        explicit dev override is present."""
        if not self.pii_boundary_strict and os.environ.get(
            "INSIGHTD_DEV_DISABLE_PII_BOUNDARY"
        ) != "1":
            raise RuntimeError(
                "PII boundary disabled (pii_boundary_strict=False) without "
                "INSIGHTD_DEV_DISABLE_PII_BOUNDARY=1 — refusing to start."
            )


def _load(module: str, cls: str):
    """Import an adapter class by string at call time (keeps heavy deps lazy)."""
    mod = importlib.import_module(f".adapters.{module}", package="insightd")
    return getattr(mod, cls)


def active_adapter_set(settings: Settings) -> dict[str, str]:
    """Report which adapter (fake|real) is active per stage, for /healthz."""
    return {
        "ingest": "real" if settings.use_real_ingest else "fake",
        "ocr": "real" if settings.use_real_ocr else "fake",
        "pii": "real" if settings.use_real_pii else "fake",
        "classifier": "real" if settings.use_real_classifier else "fake",
        "inferencer": "real" if settings.use_real_inferencer else "fake",
        "mapper": "pure",  # mapper is always the one pure implementation
    }


class StageFactory:
    """Builds the six stage instances, fakes by default, reals by string."""

    def __init__(self, settings: Settings) -> None:
        self.settings = settings

    # -- core / always-pure stages -----------------------------------------

    def mapper(self) -> "VoucherDraftMapper":
        from .adapters.mapper import PureVoucherDraftMapper

        return PureVoucherDraftMapper(ruleset_version=self.settings.ruleset_version)

    # -- swappable stages ---------------------------------------------------

    def ingest(self) -> "Ingest":
        if self.settings.use_real_ingest:
            return _load("ingest_real", "RealIngest")()
        from .adapters.fakes import FakeIngest

        return FakeIngest()

    def ocr(self) -> "OCRExtractor":
        if self.settings.use_real_ocr:
            return _load("ocr_paddle", "PaddleOCRExtractor")()
        from .adapters.fakes import FakeOCRExtractor

        return FakeOCRExtractor()

    def pii(self) -> "PIIRedactor":
        if self.settings.use_real_pii:
            return _load("pii_presidio", "PresidioPIIRedactor")()
        from .adapters.fakes import FakePIIRedactor

        return FakePIIRedactor()

    def classifier(self, boundary) -> "Classifier":
        if self.settings.use_real_classifier:
            return _load("classifier_haiku", "HaikuClassifier")(
                boundary=boundary, model=self.settings.classifier_model
            )
        from .adapters.fakes import FakeClassifier

        return FakeClassifier(boundary=boundary)

    def inferencer(self, boundary) -> "Inferencer":
        if self.settings.use_real_inferencer:
            return _load("inferencer_opus", "OpusInferencer")(
                boundary=boundary, model=self.settings.inferencer_model
            )
        from .adapters.fakes import FakeInferencer

        return FakeInferencer(boundary=boundary)


_settings: Settings | None = None


def get_settings() -> Settings:
    """Process-wide settings singleton."""
    global _settings
    if _settings is None:
        _settings = Settings()
        _settings.validate_boundary()
    return _settings
