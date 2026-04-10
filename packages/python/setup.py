import os
from setuptools import setup, Extension

setup(
    name="replicate-vfs",
    version=os.environ.get("REPLICATE_VERSION", "0.0.0"),
    description="Replicate VFS extension for SQLite",
    long_description=open("README.md").read(),
    long_description_content_type="text/markdown",
    url="https://github.com/hanzoai/replicate",
    license="Apache-2.0",
    packages=["replicate_vfs"],
    package_data={"replicate_vfs": ["*.so", "*.dylib"]},
    ext_modules=[Extension("replicate_vfs._noop", ["replicate_vfs/noop.c"])],
    python_requires=">=3.8",
    classifiers=[
        "Development Status :: 4 - Beta",
        "Intended Audience :: Developers",
        "License :: OSI Approved :: Apache Software License",
        "Programming Language :: Python :: 3",
        "Topic :: Database",
    ],
)
