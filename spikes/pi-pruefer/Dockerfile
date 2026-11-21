FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    CUDA_VISIBLE_DEVICES="" \
    OMP_NUM_THREADS=1 \
    MKL_NUM_THREADS=1 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1
WORKDIR /w/spikes/pi-pruefer
COPY requirements.txt ./
RUN python -m pip install --no-cache-dir --require-hashes -r requirements.txt
COPY . ./
CMD ["python", "-B", "messen.py", "--kandidat", "schlagwort"]
