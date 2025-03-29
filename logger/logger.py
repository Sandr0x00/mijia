#!/usr/bin/env python3

import asyncio
import json
import logging
import os
import pathlib
import struct
from typing import Any

from bleak import BleakScanner
from bleak.backends.device import BLEDevice
from bleak.backends.scanner import AdvertisementData
import aiosqlite

# so execution is not dependent on directory but only the structure in the git
file_path = pathlib.Path(os.path.dirname(__file__))
log_dir = file_path / ".." / "logs"
config_dir = file_path / ".."

logger = logging.getLogger(__name__)
logging.basicConfig(
    filename=file_path / "error.log",
    format="%(asctime)s %(levelname)s: %(message)s",
    encoding="utf-8",
    level=logging.INFO,
)

config: dict[str, Any] = {}
lock = asyncio.Lock()

async def callback(device: BLEDevice, advertising_data: AdvertisementData):
    # print(device)
    # print(advertising_data)
    # print(device.address)
    # logger.info(device)
    mac = device.address.lower()
    if mac not in config:
        # we only support mijia data
        return

    if len(advertising_data.service_uuids) != 1:
        # something wrong, ignore
        logging.error(f"Invalid amount of Service UUIDs {advertising_data.service_uuids}")
        return

    service_uuid = advertising_data.service_uuids[0]

    data = advertising_data.service_data[service_uuid]
    # print(data.hex())

    if len(data) != 15:
        logger.error(f"Invalid length {data.hex()}")
        return

    _mac = data[:6][::-1]

    # parse data | https://github.com/bentolor/xiaomi-mijia-bluetooth-firmware/blob/master/src/ble.h#L100
    temp, humidity, battery_mv, battery_level, counter, _flags = struct.unpack("<HHHBBB", data[6:])
    # print(device.address, f"{temp=} {humidity=} {battery_mv=} {battery_level=} {counter=}")

    if config[mac]["counter"] != counter:
        # new event, store to db
        async with aiosqlite.connect(log_dir / f"{mac}.db") as db:
            await db.execute(
                "INSERT INTO sensor_data (temp, humidity, battery_mv, battery_level) VALUES (?, ?, ?, ?)",
                (
                    temp,
                    humidity,
                    battery_mv,
                    battery_level
                )
            )
            await db.commit()

        # update counter
        async with lock:
            config[mac]["counter"] = counter


async def main():
    stop_event = asyncio.Event()

    for mac in config:
        config[mac]["counter"] = 0
        async with aiosqlite.connect(log_dir / f"{mac}.db") as db:
            await db.execute("""
                CREATE TABLE IF NOT EXISTS sensor_data (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    temp INTEGER NOT NULL,
                    humidity INTEGER NOT NULL,
                    battery_mv INTEGER NOT NULL,
                    battery_level INTEGER NOT NULL,
                    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
                )
            """)
            await db.commit()

    # Ensure the database has the correct table structure

    async with BleakScanner(callback):
        print("Listening for Mijia Advertisement Data ...")
        await stop_event.wait()


if __name__ == '__main__':
    with open(config_dir / "config.json", "r", encoding="utf-8") as f:
        config = json.loads(f.read())

    asyncio.run(main())
