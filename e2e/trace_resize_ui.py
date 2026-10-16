def value(handle):
    return int(handle.get_attribute('aria-valuenow'))


def drag_column(page, handle, delta):
    bounds = handle.bounding_box()
    before = value(handle)
    expected = min(int(handle.get_attribute('aria-valuemax')), max(int(handle.get_attribute('aria-valuemin')), before + delta))
    page.mouse.move(bounds['x'] + bounds['width'] / 2, bounds['y'] + bounds['height'] / 2)
    page.mouse.down()
    page.mouse.move(bounds['x'] + bounds['width'] / 2 + delta, bounds['y'] + bounds['height'] / 2, steps=12)
    page.mouse.up()
    page.wait_for_function('(args) => Math.abs(Number(document.querySelector(`[aria-label="${args[0]}"]`).getAttribute("aria-valuenow")) - args[1]) <= 1', arg=[handle.get_attribute('aria-label'), expected])
    return value(handle)


def check_list_resize(page, narrow):
    page.locator('[role=status][aria-busy]').wait_for(state='detached')
    handles = page.get_by_role('separator')
    assert handles.count() == (2 if narrow else 4)
    first = page.get_by_role('separator', name='Resize Service / operation column', exact=True)
    url = page.url
    initial = value(first)
    first.focus()
    first.press('Home')
    assert value(first) == int(first.get_attribute('aria-valuemin'))
    first.press('End')
    assert value(first) == int(first.get_attribute('aria-valuemax'))
    first.press('Shift+ArrowLeft')
    first.press('ArrowRight')
    drag_column(page, first, initial - value(first) + (-12 if narrow else 64))
    assert value(first) != initial
    assert page.url == url
    headers = page.locator('.trace-results thead th:visible')
    cells = page.locator('.trace-results tbody tr').first.locator('td:visible')
    assert headers.count() == cells.count() == (3 if narrow else 5)
    for index in range(headers.count()):
        head, cell = headers.nth(index).bounding_box(), cells.nth(index).bounding_box()
        assert abs(head['x'] - cell['x']) < 1 and abs(head['width'] - cell['width']) < 1
    saved = value(first)
    page.reload()
    first.wait_for()
    page.locator('[role=status][aria-busy]').wait_for(state='detached')
    page.wait_for_function('(expected) => Number(document.querySelector(`[aria-label="Resize Service / operation column"]`).getAttribute("aria-valuenow")) === expected', arg=saved)
    return saved


def check_waterfall_resize(page, narrow):
    first = page.get_by_role('separator', name='Resize waterfall name column', exact=True)
    first.wait_for()
    page.locator('[role=status][aria-busy]').wait_for(state='detached')
    timeline = page.get_by_role('separator', name='Resize waterfall timeline column', exact=True)
    url = page.url
    initial = value(first)
    drag_column(page, first, -12 if narrow else 120)
    assert value(first) != initial
    timeline.focus()
    timeline.press('Home')
    assert value(timeline) == int(timeline.get_attribute('aria-valuemin'))
    timeline.press('End')
    assert value(timeline) == int(timeline.get_attribute('aria-valuemax'))
    timeline.press('Shift+ArrowLeft')
    timeline.press('ArrowRight')
    axis = page.locator('.trace-axis').bounding_box()
    timing = page.locator('.trace-span-time').first.bounding_box()
    name = page.locator('.trace-ruler-label').bounding_box()
    row_name = page.locator('.trace-span-name').first.bounding_box()
    assert abs(axis['x'] - timing['x']) < 1 and abs(axis['width'] - timing['width']) < 1
    assert abs(name['width'] - row_name['width']) < 1
    assert page.url == url
    expanded = page.locator('.trace-span-row').count()
    toggle = page.locator('.trace-span-rows button[aria-expanded="true"]').first
    toggle.click()
    assert page.locator('.trace-span-row').count() < expanded
    page.get_by_role('button', name='Expand all', exact=True).click()
    assert page.locator('.trace-span-row').count() == expanded
    check_resize_cancellation(page, first)
    return value(first)


def check_resize_cancellation(page, handle):
    for cancel in ('pointercancel', 'lostcapture', 'Escape'):
        before = value(handle)
        before_text = handle.get_attribute('aria-valuetext')
        stored = page.evaluate('localStorage.getItem("loco:trace-waterfall-columns:v1")')
        bounds = handle.bounding_box()
        x, y = bounds['x'] + bounds['width'] / 2, bounds['y'] + bounds['height'] / 2
        direction = -1 if before > int(handle.get_attribute('aria-valuemin')) else 1
        page.mouse.move(x, y)
        page.mouse.down()
        page.mouse.move(x + direction * 24, y, steps=4)
        changed = value(handle)
        assert changed != before
        for primary in (False, True):
            handle.dispatch_event('pointerdown', {'pointerId': 2, 'isPrimary': primary, 'button': 0, 'clientX': x})
            handle.dispatch_event('pointermove', {'pointerId': 2, 'clientX': x + 100})
            handle.dispatch_event('pointercancel', {'pointerId': 2})
            assert value(handle) == changed
        if cancel == 'pointercancel':
            handle.dispatch_event('pointercancel', {'pointerId': 1})
        elif cancel == 'lostcapture':
            handle.evaluate('(element) => element.releasePointerCapture(1)')
            page.mouse.move(x + direction * 25, y)
        else:
            handle.press('Escape')
        page.wait_for_function('(args) => Number(document.querySelector(`[aria-label="${args[0]}"]`).getAttribute("aria-valuenow")) === args[1]', arg=[handle.get_attribute('aria-label'), before])
        assert handle.get_attribute('aria-valuetext') == before_text
        assert not handle.evaluate('(element) => element.hasPointerCapture(1)')
        actual = page.locator('.trace-span-name').first.bounding_box()['width']
        assert abs(actual - before) <= 1
        assert page.evaluate('localStorage.getItem("loco:trace-waterfall-columns:v1")') == stored
        page.mouse.up()
