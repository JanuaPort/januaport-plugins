FROM python:3.12-slim AS basis

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    CUDA_VISIBLE_DEVICES="" \
    OMP_NUM_THREADS=4 \
    MKL_NUM_THREADS=4 \
    HF_HOME=/hf \
    HF_HUB_DISABLE_IMPLICIT_TOKEN=1 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1
WORKDIR /w/spikes/pi-pruefer

FROM basis AS strands
COPY requirements-strands.txt ./
RUN python -m pip install --no-cache-dir --require-hashes -r requirements-strands.txt
COPY . ./
CMD ["python", "-B", "phase2.py", "--kandidat", "strands_decider"]

FROM basis AS strands_gpu
ENV CUDA_VISIBLE_DEVICES=0
COPY requirements-gpu.txt ./
RUN python -m pip install --no-cache-dir --require-hashes -r requirements-gpu.txt
COPY . ./
CMD ["python", "-B", "phase3.py", "--kandidat", "strands_decider", "--hardware", "gpu_bf16"]

FROM basis AS klassifikatoren
COPY requirements.txt ./
RUN python -m pip install --no-cache-dir --require-hashes -r requirements.txt
COPY . ./
CMD ["python", "-B", "messen.py", "--kandidat", "schlagwort"]
