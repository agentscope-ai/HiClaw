from setuptools import setup, find_packages

setup(
    name="copaw-worker",
    version="0.1.0",
    packages=find_packages(where="src"),
    package_dir={"": "src"},
    install_requires=[
        "matrix-nio>=0.24.0",
        "httpx>=0.27.0",
        "pydantic>=2.0.0",
        "pydantic-settings>=2.0.0",
        "rich>=13.0.0",
        "typer>=0.12.0",
    ],
    entry_points={
        "console_scripts": [
            "copaw-worker=copaw_worker.cli:app",
        ],
    },
    python_requires=">=3.10",
)
