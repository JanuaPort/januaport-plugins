"""Allow both python -m unittest and explicit discover -s tests, as in CI."""
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
