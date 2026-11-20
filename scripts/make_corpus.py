"""Writes corpus/, the Winlink-style messages b2trace compress is measured on.

Everything in it was written for this repository: the prose messages below
by hand, the forms and reports filled with made-up data. Callsigns are N0
calls with four-letter suffixes, which are never issued, and names are roles,
not people. Messages are laid out the way Pat writes them. The committed
corpus/ is the reference; another Python version may draw different random
values.
"""

import os
import random
import shutil

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "corpus")
rng = random.Random(2026)

# The stations of one exercise, which pass forms and reports between them.
STATIONS = ["N0EOCA", "N0EOCB", "N0FLDA", "N0FLDB", "N0FLDC", "N0SHLA", "N0SHLB", "N0HOSP", "N0RLYA", "N0RLYB"]
PLACES = ["North Shelter", "County EOC", "Fire Station 4", "High School Gym", "Water Plant",
          "Fairgrounds Staging", "Regional Hospital", "Library Annex", "Public Works Yard", "Hilltop Repeater Site"]
ROLES = ["Shelter Manager", "Logistics Chief", "EOC Director", "Net Control", "Planning Section",
         "Medical Unit Leader", "Public Works Liaison", "Radio Officer", "Operations Chief", "Supply Unit"]

# Written by hand for this corpus, with their subjects.
PROSE = [
    ("Home", "Made it home fine. Roads were clear after the pass."),
    ("Running late", "Running about an hour late, will check in when I reach the trailhead."),
    ("Saturday drill", "Can you bring the spare battery and the 25 foot coax to Saturday's drill? Mine has a bad connector."),
    ("Antenna work moved", "Reminder: the antenna party moved to Sunday because of the wind forecast. Same time, bring gloves."),
    ("Generator", "Got your message. The generator started on the second pull and has been running since 0600.\r\n"
     "Fuel looks good for another eight hours. I will top it off at noon."),
    ("North road", "The north road is washed out about two miles past the bridge. Do not try it with the trailer.\r\n"
     "We came around by the county road instead, adds about forty minutes. Everyone here is fine."),
    ("Last night", "Thanks for relaying last night. Signal reports were weak on 40 meters but the message got through "
     "on the second try. Let's try 30 meters next week and compare."),
    ("We are okay", "Water is still off at the house. Neighbors are sharing the well pump on the hill, so we have enough "
     "for drinking and cooking. Please let the family know we are okay and not to drive up yet."),
    ("Practice traffic", "Practice message for the Thursday net. Please acknowledge receipt with the time you got it and the "
     "mode you used, so we can compare delivery times across VARA, ARDOP and packet."),
    ("Shelter inventory", "Inventory from this morning: 14 cots set up, 6 still in boxes, 40 blankets, 3 cases of water, "
     "2 first aid kits, one kit missing gauze. Need more paper towels and trash bags by tonight."),
    ("Exercise site", "Change of plan for the field exercise. We will set up at the fairgrounds instead of the school,\r\n"
     "because the school parking lot is being resurfaced. Power is available at the north barn.\r\n"
     "Talk-in on the usual repeater. Bring your own table if you can."),
    ("Test", "Hi, just testing the new mailbox setup. If this arrives, the gateway settings are right and you can "
     "delete it. No reply needed."),
    ("Shelter tonight", "Long day. The shelter count went from 31 to 52 after the evacuation order expanded. Kitchen crew "
     "is stretched thin and asked for two more volunteers for the dinner shift. Medical had three minor "
     "cases, all treated on site. Pets are in the side building and that is working well. Tomorrow we "
     "expect the count to drop once the road reopens, but nobody has confirmed a time yet. I will send "
     "the formal report on the form at 1800."),
    ("Net control dates", "Please forward to the net manager: I can cover net control on the 12th and 26th but not the 19th."),
    ("Battery", "Battery bank is at 12.4 volts with the solar panel connected. Station draws about 2 amps receiving "
     "and 18 on transmit. Should last through the night if I keep transmissions short."),
    ("Re: briefing", "Message received, thanks. Will pass it on at the morning briefing."),
]


def mid():
    return "".join(rng.choice("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") for _ in range(12))


def fresh_call():
    return "N0" + "".join(rng.choice("ABCDEFGHIJKLMNOPQRSTUVWXYZ") for _ in range(4))


def stamp(day, minute):
    return "2026/11/%02d %02d:%02d" % (day, minute // 60, minute % 60)


# Pat's layout: Mid and Body first, then the other headers sorted, a blank
# line, the body, and each attachment after a CRLF.
def message(frm, to, subject, body, day, minute, files=(), kind="Private"):
    body = body.replace("\r\n", "\n").replace("\n", "\r\n") + "\r\n"
    data = body.encode("latin-1")
    head = [
        "Mid: " + mid(),
        "Body: %d" % len(data),
        "Content-Transfer-Encoding: 8bit",
        "Content-Type: text/plain; charset=ISO-8859-1",
        "Date: " + stamp(day, minute),
    ]
    for name, content in files:
        head.append("File: %d %s" % (len(content), name))
    head += ["From: " + frm, "Mbo: " + frm, "Subject: " + subject, "To: " + to, "Type: " + kind]
    out = ("\r\n".join(head) + "\r\n\r\n").encode("latin-1") + data
    for _, content in files:
        out += b"\r\n" + content
    return out + b"\r\n" if files else out


def ics213(i):
    frm, to = rng.sample(STATIONS, 2)
    place = rng.choice(PLACES)
    role_from, role_to = rng.sample(ROLES, 2)
    need = rng.choice([
        "Request 20 additional cots and 40 blankets for overnight arrivals.",
        "Generator at %s needs 15 gallons of diesel before 2200." % place,
        "Two volunteers needed for the 0600 to 1200 radio shift.",
        "Confirm the road closure on Route %d is still in effect." % rng.randint(2, 60),
        "Shelter population now %d. Food on hand for %d more meals." % (rng.randint(10, 90), rng.randint(20, 300)),
        "Request a status update on the water plant pumps.",
        "Please send the updated contact list for all shelters.",
    ])
    day, minute = rng.randint(1, 28), rng.randint(0, 1439)
    fields = [
        ("To", role_to), ("From", role_from), ("Subject", "Resource request %d" % (i + 1)),
        ("Date", stamp(day, minute)[:10]), ("Time", stamp(day, minute)[11:]),
        ("Message", need), ("Approved_Name", role_from), ("Approved_Position", "Section lead"),
        ("Incident", "Winter storm exercise"), ("Location", place),
    ]
    text = "GENERAL MESSAGE (ICS 213)\r\n\r\n" + "\r\n".join("%s: %s" % (k.replace("_", " "), v) for k, v in fields)
    xml = ['<?xml version="1.0" encoding="UTF-8"?>', "<RMS_Express_Form>", "  <form_parameters>",
           "    <xml_file_version>1.0</xml_file_version>", "    <display_form>ICS213_Viewer.html</display_form>",
           "    <reply_template>ICS213_Reply.txt</reply_template>", "  </form_parameters>", "  <variables>"]
    xml += ["    <%s>%s</%s>" % (k.lower(), v, k.lower()) for k, v in fields]
    xml += ["  </variables>", "</RMS_Express_Form>"]
    attach = ("\r\n".join(xml) + "\r\n").encode("utf-8")
    return message(frm, to, "ICS 213: Resource request %d" % (i + 1), text, day, minute,
                   [("RMS_Express_Form_ICS213_Viewer.xml", attach)])


# The fields and formats of wl2k-go's catalog.PosReport.
def position(i):
    frm = rng.choice(STATIONS)
    lat, lon = 39.5 + rng.random(), -105.5 + rng.random()
    day, minute = rng.randint(1, 28), rng.randint(0, 1439)

    def dm(v, pos, neg, width):
        h = pos if v >= 0 else neg
        v = abs(v)
        return "%0*d-%07.4f%s" % (width, int(v), (v - int(v)) * 60, h)

    lines = [
        "DATE: " + stamp(day, minute),
        "LATITUDE: " + dm(lat, "N", "S", 2),
        "LONGITUDE: " + dm(lon, "E", "W", 3),
        "SPEED: %f" % rng.randint(0, 65),
        "COURSE: %03dT" % rng.randint(0, 359),
    ]
    comment = rng.choice(["Mobile, en route to staging", "At shelter", "Portable, hilltop",
                          "Parked, monitoring", "Returning to base", ""])
    if comment:
        lines.append("COMMENT: " + comment)
    return message(frm, "QTH", "POSITION REPORT", "\r\n".join(lines), day, minute, kind="Position Report")


def radiogram(i):
    frm, to = fresh_call(), fresh_call()
    words = rng.choice([
        "ALL SAFE AT SHELTER NO NEED TO TRAVEL WILL CALL WHEN PHONES RETURN",
        "ARRIVED AT STAGING WITH SUPPLIES AND THREE VOLUNTEERS READY TO WORK",
        "PLEASE CONFIRM NET TIME CHANGE TO NINETEEN HUNDRED THURSDAYS",
        "POWER RESTORED AT WATER PLANT PUMPS RUNNING NORMAL",
        "REQUEST CHECK ON WELFARE OF RESIDENTS AT PINE STREET ADDRESS",
    ])
    day, minute = rng.randint(1, 28), rng.randint(0, 1439)
    body = "\r\n".join([
        "NR %d R HX%s %s %d ANYTOWN CO NOV %d" % (i + 1, rng.choice("ABCDEFG"), frm, len(words.split()), day),
        "TO", rng.choice(ROLES).upper(), rng.choice(PLACES).upper(), "BT", words, "BT",
        rng.choice(ROLES).upper() + " " + frm,
    ])
    return message(frm, to, "RADIOGRAM NR %d" % (i + 1), body, day, minute)


def checkin(i):
    frm = rng.choice(STATIONS)
    day, minute = rng.randint(1, 28), rng.randint(1080, 1200)
    body = "%s checking in. %s, %s power, %s." % (
        frm, rng.choice(["Home station", "Portable", "Mobile", "Field site"]),
        rng.choice(["mains", "battery", "solar", "generator"]),
        rng.choice(["no traffic", "one message for the EOC", "all OK here", "listening only"]))
    return message(frm, "N0EOCA", "Net check-in", body, day, minute)


def weather(i):
    frm = rng.choice(STATIONS)
    day, minute = rng.randint(1, 28), rng.randint(0, 1439)
    body = "\r\n".join([
        "SPOTTER REPORT",
        "Time: " + stamp(day, minute),
        "Location: %d miles %s of %s" % (rng.randint(1, 15), rng.choice(["N", "NE", "E", "SE", "S", "SW", "W", "NW"]), rng.choice(PLACES)),
        "Snow: %.1f in in last 6 hours, %d in on ground" % (rng.random() * 8, rng.randint(1, 30)),
        "Wind: %s %d mph, gusts %d" % (rng.choice(["N", "NW", "W"]), rng.randint(5, 30), rng.randint(20, 55)),
        "Visibility: %s" % rng.choice(["under 1/4 mile", "about 1 mile", "3 miles", "unrestricted"]),
        "Remarks: " + rng.choice(["Drifting on county roads.", "Power flickering.", "Trees down on Route 9.", "None."]),
    ])
    return message(frm, "N0EOCA", "Weather spotter report", body, day, minute)


# Personal mail goes between all kinds of stations, not just the exercise's.
def prose(subject, text):
    day, minute = rng.randint(1, 28), rng.randint(0, 1439)
    return message(fresh_call(), fresh_call(), subject, text, day, minute)


def main():
    shutil.rmtree(OUT, ignore_errors=True)
    os.makedirs(OUT)
    kinds = [("ics213", ics213, 14), ("position", position, 14), ("radiogram", radiogram, 10),
             ("checkin", checkin, 10), ("weather", weather, 10)]
    n = 0
    for name, make, count in kinds:
        for i in range(count):
            with open(os.path.join(OUT, "%s-%02d.b2f" % (name, i + 1)), "wb") as f:
                f.write(make(i))
            n += 1
    for i, (subject, text) in enumerate(PROSE):
        with open(os.path.join(OUT, "prose-%02d.b2f" % (i + 1)), "wb") as f:
            f.write(prose(subject, text))
        n += 1
    print("wrote %d messages to %s" % (n, os.path.normpath(OUT)))


if __name__ == "__main__":
    main()
